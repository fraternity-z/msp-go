package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	documentparse "mathstudy/backend/internal/adapter/documentparse"
	openaicompat "mathstudy/backend/internal/adapter/llm/openaicompat"
	resourceretrieval "mathstudy/backend/internal/adapter/llm/resourceretrieval"
	adapterpostgres "mathstudy/backend/internal/adapter/postgres"
	qdrantadapter "mathstudy/backend/internal/adapter/qdrant"
	storageadapter "mathstudy/backend/internal/adapter/storage"
	adminaiconfig "mathstudy/backend/internal/application/adminaiconfig"
	adminstorage "mathstudy/backend/internal/application/adminstorage"
	resourceapp "mathstudy/backend/internal/application/resource"
	"mathstudy/backend/internal/platform/config"
	"mathstudy/backend/internal/platform/outbound"
	platformpostgres "mathstudy/backend/internal/platform/postgres"
	"mathstudy/backend/internal/platform/secret"
)

type options struct {
	command, listen, pdfinfo, pdftotext, generationID, knowledgeBaseID string
	apply                                                              bool
	concurrency, maxPages                                              int
	reconcileInterval, timeout                                         time.Duration
}

type ingestionRunner interface {
	Run(context.Context) error
	Reconcile(context.Context, resourceapp.IngestionGeneration, bool, int) (resourceapp.IngestionReconcileReport, error)
	Metrics() string
}

type maintenanceRepository interface {
	ListIngestionGenerations(context.Context, string, int) ([]resourceapp.IngestionGeneration, error)
	BeginIngestionRebuild(context.Context, string, string, time.Time) (resourceapp.IngestionGeneration, error)
	IngestionQueueStats(context.Context) (map[string]int64, error)
	CleanupIngestionHistory(context.Context, time.Time, int) (resourceapp.IngestionHistoryCleanup, error)
}

type runtime struct {
	worker       ingestionRunner
	repo         maintenanceRepository
	models       resourceapp.IngestionModelProvider
	ping         func(context.Context) error
	close        func()
	maintenance  *maintenanceMetrics
	deleteObject func(context.Context, resourceapp.ObjectSource) error
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(logger)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := execute(ctx, os.Args[1:], os.Stdout, os.Stderr, newRuntime); err != nil && !errors.Is(err, flag.ErrHelp) && !errors.Is(err, context.Canceled) {
		logger.Error("vector worker stopped", "error_code", "worker_unavailable")
		os.Exit(1)
	}
}

func parseOptions(args []string, output io.Writer) (options, error) {
	o := options{command: "run"}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		o.command, args = args[0], args[1:]
	}
	flags := flag.NewFlagSet("vector-worker", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&o.listen, "listen", "127.0.0.1:8091", "loopback health and metrics address")
	flags.StringVar(&o.pdfinfo, "pdfinfo", "pdfinfo", "PDF metadata executable")
	flags.StringVar(&o.pdftotext, "pdftotext", "pdftotext", "PDF text executable")
	flags.IntVar(&o.concurrency, "concurrency", 2, "parallel document jobs (1-8)")
	flags.IntVar(&o.maxPages, "max-pages", 200, "maximum pages per reconcile phase (1-10000)")
	flags.DurationVar(&o.reconcileInterval, "reconcile-interval", 5*time.Minute, "automatic reconciliation interval")
	flags.DurationVar(&o.timeout, "timeout", 2*time.Minute, "bounded maintenance operation timeout")
	flags.StringVar(&o.generationID, "generation", "", "explicit generation UUID for reconciliation")
	flags.StringVar(&o.knowledgeBaseID, "knowledge-base", "", "explicit knowledge-base UUID")
	flags.BoolVar(&o.apply, "apply", false, "apply scoped maintenance changes; otherwise report only")
	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: vector-worker [run|reconcile|rebuild] [flags]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return o, err
	}
	if flags.NArg() != 0 {
		return o, errors.New("unexpected positional arguments")
	}
	if o.command != "run" && o.command != "reconcile" && o.command != "rebuild" {
		return o, errors.New("unknown worker command")
	}
	if o.concurrency < 1 || o.concurrency > 8 || o.maxPages < 1 || o.maxPages > 10000 || o.timeout < time.Second || o.timeout > 10*time.Minute || o.reconcileInterval < time.Second {
		return o, errors.New("worker limits are invalid")
	}
	host, _, err := net.SplitHostPort(o.listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return o, errors.New("worker management address must be loopback")
	}
	for _, id := range []string{o.generationID, o.knowledgeBaseID} {
		if id != "" {
			parsed, err := uuid.Parse(id)
			if err != nil || parsed.String() != id {
				return o, errors.New("scope must use canonical UUIDs")
			}
		}
	}
	if o.command == "run" && (o.apply || o.generationID != "" || o.knowledgeBaseID != "") {
		return o, errors.New("maintenance flags require a maintenance command")
	}
	if o.command == "rebuild" && (o.knowledgeBaseID == "" || o.generationID != "") {
		return o, errors.New("rebuild requires --knowledge-base and uses the active administrator model")
	}
	if o.command == "reconcile" && o.apply && o.generationID == "" {
		return o, errors.New("reconcile --apply requires --generation")
	}
	return o, nil
}

func execute(ctx context.Context, args []string, output, diagnostics io.Writer, factory func(context.Context, options) (runtime, error)) error {
	o, err := parseOptions(args, diagnostics)
	if err != nil {
		return err
	}
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	rt, err := factory(startup, o)
	cancel()
	if err != nil {
		return err
	}
	defer rt.close()
	if o.command == "run" {
		return serveWorker(ctx, rt, o)
	}
	operation, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	return runMaintenance(operation, rt, o, output)
}

func newRuntime(ctx context.Context, o options) (runtime, error) {
	cfg, err := config.Load()
	if err != nil {
		return runtime{}, errors.New("worker configuration is invalid")
	}
	return runtimeFromConfig(ctx, cfg, o)
}

func runtimeFromConfig(ctx context.Context, cfg config.Config, o options) (runtime, error) {
	if !cfg.QdrantEnabled {
		return runtime{}, errors.New("QDRANT_ENABLED must be true for the vector worker")
	}
	if strings.TrimSpace(cfg.FernetSecretKey) == "" {
		return runtime{}, errors.New("FERNET_SECRET_KEY must match the API configuration")
	}
	cipher, err := secret.NewFernet(cfg.FernetSecretKey)
	if err != nil {
		return runtime{}, errors.New("worker cipher configuration is invalid")
	}
	pool, err := platformpostgres.NewPool(ctx, cfg)
	if err != nil {
		return runtime{}, errors.New("worker database configuration is invalid")
	}
	ok := false
	defer func() {
		if !ok {
			pool.Close()
		}
	}()
	repo, err := adapterpostgres.NewResourceRepository(pool)
	if err != nil {
		return runtime{}, err
	}
	aiRepo, err := adapterpostgres.NewAdminAIConfigRepository(pool)
	if err != nil {
		return runtime{}, err
	}
	providerClient := outbound.NewPublicHTTPSClient(300 * time.Second)
	ai, err := adminaiconfig.NewService(aiRepo, cipher, openaicompat.WrapClient(providerClient))
	if err != nil {
		return runtime{}, errors.New("worker model configuration is invalid")
	}
	models, err := resourceretrieval.NewDocumentEmbedder(ai)
	if err != nil {
		return runtime{}, err
	}
	objects := storageadapter.NewRuntimeManager(cfg.UploadsDir)
	settingsRepo, err := adapterpostgres.NewAdminSettingsRepository(pool)
	if err != nil {
		return runtime{}, err
	}
	storage, err := adminstorage.NewService(settingsRepo, cipher, objects, objects.LocalConfigured())
	if err != nil {
		return runtime{}, errors.New("worker storage configuration is invalid")
	}
	if err := storage.ActivateStored(ctx); err != nil {
		return runtime{}, errors.New("worker storage configuration is unavailable")
	}
	parser, err := documentparse.New(documentparse.Config{PDFInfoPath: o.pdfinfo, PDFToTextPath: o.pdftotext})
	if err != nil {
		return runtime{}, err
	}
	index, err := qdrantadapter.New(qdrantadapter.Config{BaseURL: cfg.QdrantURL, APIKey: cfg.QdrantAPIKey, Collection: cfg.QdrantCollection,
		Timeout: cfg.QdrantTimeout, HealthTimeout: cfg.QdrantHealthTimeout, MaxBatchSize: cfg.QdrantMaxBatchSize, WaitForChanges: true, PayloadIndexes: cfg.QdrantPayloadIndexFields}, qdrantadapter.WithResourceCollections())
	if err != nil {
		return runtime{}, errors.New("worker vector configuration is invalid")
	}
	reader := refreshingObjectReader{objects: objects, refresh: storage.ActivateStored}
	worker, err := resourceapp.NewIngestionWorker(repo, reader, parser, resourceapp.NewDeterministicChunker(), models, models, index, slog.Default(),
		resourceapp.IngestionWorkerConfig{Concurrency: o.concurrency, JobTimeout: 10 * time.Minute})
	if err != nil {
		return runtime{}, err
	}
	ok = true
	return runtime{worker: worker, repo: repo, models: models, close: pool.Close, maintenance: &maintenanceMetrics{},
		deleteObject: func(ctx context.Context, source resourceapp.ObjectSource) error {
			if err := storage.ActivateStored(ctx); err != nil {
				return resourceapp.ErrIngestionUnavailable
			}
			return objects.DeleteDocument(ctx, source)
		}, ping: func(ctx context.Context) error {
			if err := pool.Ping(ctx); err != nil {
				return resourceapp.ErrIngestionUnavailable
			}
			return index.Ping(ctx)
		}}, nil
}

type refreshingObjectReader struct {
	objects resourceapp.ObjectReader
	refresh func(context.Context) error
}

func (r refreshingObjectReader) Open(ctx context.Context, source resourceapp.ObjectSource) (io.ReadCloser, resourceapp.ObjectMetadata, error) {
	if err := r.refresh(ctx); err != nil {
		if ctx.Err() != nil {
			return nil, resourceapp.ObjectMetadata{}, ctx.Err()
		}
		return nil, resourceapp.ObjectMetadata{}, resourceapp.ErrIngestionUnavailable
	}
	return r.objects.Open(ctx, source)
}

func runMaintenance(ctx context.Context, rt runtime, o options, output io.Writer) error {
	encoder := json.NewEncoder(output)
	if o.command == "rebuild" {
		model, _, err := rt.models.CurrentModel(ctx)
		if err != nil {
			return resourceapp.ErrIngestionModelUnavailable
		}
		if !o.apply {
			return encoder.Encode(map[string]any{"command": "rebuild", "dry_run": true, "knowledge_base_id": o.knowledgeBaseID, "model_version_id": model.ID})
		}
		g, err := rt.repo.BeginIngestionRebuild(ctx, o.knowledgeBaseID, model.ID, time.Now().UTC())
		if err != nil {
			return err
		}
		return encoder.Encode(map[string]any{"command": "rebuild", "dry_run": false, "knowledge_base_id": g.KnowledgeBaseID, "generation_id": g.ID, "generation": g.Number, "state": g.State})
	}
	after := ""
	matched := 0
	for page := 0; page < 10000; page++ {
		generations, err := rt.repo.ListIngestionGenerations(ctx, after, 100)
		if err != nil {
			return err
		}
		for _, g := range generations {
			if o.generationID != "" && o.generationID != g.ID || o.knowledgeBaseID != "" && o.knowledgeBaseID != g.KnowledgeBaseID {
				continue
			}
			report, err := reconcileObserved(ctx, rt, g, o.apply, o.maxPages)
			if err != nil {
				return err
			}
			if err := encoder.Encode(report); err != nil {
				return err
			}
			matched++
		}
		if len(generations) < 100 {
			if o.generationID != "" && matched == 0 {
				return errors.New("generation scope was not found")
			}
			return nil
		}
		after = generations[len(generations)-1].ID
	}
	return errors.New("generation scan exceeded its bounded page count")
}

func managementHandler(rt runtime) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "application/json")
		if err := rt.ping(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, "{\"status\":\"unavailable\"}\n")
			return
		}
		_, _ = io.WriteString(w, "{\"status\":\"ok\"}\n")
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		stats, err := rt.repo.IngestionQueueStats(ctx)
		if err != nil {
			http.Error(w, "metrics unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = io.WriteString(w, rt.worker.Metrics())
		_, _ = io.WriteString(w, rt.maintenance.text())
		_, _ = io.WriteString(w, "# TYPE msp_resource_ingestion_queue_jobs gauge\n")
		for _, state := range []string{"queued", "running", "dead"} {
			_, _ = fmt.Fprintf(w, "msp_resource_ingestion_queue_jobs{state=%q} %d\n", state, stats[state])
		}
		_, _ = fmt.Fprintf(w, "# TYPE msp_resource_ingestion_oldest_wait_seconds gauge\nmsp_resource_ingestion_oldest_wait_seconds %d\n", stats["oldest_wait_seconds"])
	})
	return mux
}

func serveWorker(ctx context.Context, rt runtime, o options) error {
	if rt.maintenance == nil {
		rt.maintenance = &maintenanceMetrics{}
	}
	listener, err := net.Listen("tcp", o.listen)
	if err != nil {
		return errors.New("worker management listener is unavailable")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	server := &http.Server{Handler: managementHandler(rt), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	serverDone, workerDone, reconcileDone := make(chan error, 1), make(chan error, 1), make(chan struct{})
	go func() { serverDone <- server.Serve(listener) }()
	go func() { workerDone <- rt.worker.Run(ctx) }()
	go func() { defer close(reconcileDone); runReconciliationLoop(ctx, rt, o) }()
	var result error
	workerExited := false
	select {
	case <-ctx.Done():
	case err := <-workerDone:
		workerExited = true
		result = err
	case err := <-serverDone:
		if !errors.Is(err, http.ErrServerClosed) {
			result = errors.New("worker management server failed")
		}
	}
	cancel()
	shutdown, stop := context.WithTimeout(context.Background(), 15*time.Second)
	defer stop()
	if err := server.Shutdown(shutdown); err != nil {
		_ = server.Close()
		result = errors.New("worker management shutdown timed out")
	}
	if !workerExited {
		select {
		case err := <-workerDone:
			if err != nil {
				result = err
			}
		case <-shutdown.Done():
			return errors.New("worker shutdown timed out")
		}
	}
	select {
	case <-reconcileDone:
	case <-shutdown.Done():
		return errors.New("reconciliation shutdown timed out")
	}
	return result
}

func runReconciliationLoop(ctx context.Context, rt runtime, o options) {
	ticker := time.NewTicker(o.reconcileInterval)
	defer ticker.Stop()
	after := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			batch, cancel := context.WithTimeout(ctx, o.timeout)
			generations, err := rt.repo.ListIngestionGenerations(batch, after, 100)
			if err == nil {
				for _, g := range generations {
					if batch.Err() != nil {
						break
					}
					_, err = reconcileObserved(batch, rt, g, true, o.maxPages)
					if err != nil && batch.Err() == nil {
						slog.Warn("resource reconciliation failed", "generation_id", g.ID, "error_code", "reconcile_unavailable")
					}
					if batch.Err() == nil {
						after = g.ID
					}
				}
				if len(generations) < 100 && batch.Err() == nil {
					after = ""
				}
			}
			if err != nil && batch.Err() == nil {
				slog.Warn("resource reconciliation batch unavailable", "error_code", "repository_unavailable")
			}
			cancel()
			cleanup, stop := context.WithTimeout(ctx, time.Minute)
			runCleanupBatch(cleanup, rt)
			stop()
		}
	}
}
