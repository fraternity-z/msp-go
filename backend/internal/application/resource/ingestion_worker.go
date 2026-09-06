package resource

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"mathstudy/backend/internal/platform/identifier"
)

type IngestionVectorIndex interface {
	VectorIndex
	VectorInspector
}

type IngestionWorkerConfig struct {
	Concurrency   int
	PollInterval  time.Duration
	LeaseDuration time.Duration
	JobTimeout    time.Duration
}

type IngestionWorker struct {
	repo       IngestionRepository
	objects    ObjectReader
	parser     DocumentParser
	chunker    Chunker
	models     IngestionModelProvider
	embeddings EmbeddingProvider
	index      IngestionVectorIndex
	logger     *slog.Logger
	config     IngestionWorkerConfig
	processed  atomic.Uint64
	failed     atomic.Uint64
	leaseLost  atomic.Uint64
	inflight   atomic.Int64
	durationMS atomic.Uint64
}

func NewIngestionWorker(repo IngestionRepository, objects ObjectReader, parser DocumentParser, chunker Chunker, models IngestionModelProvider, embeddings EmbeddingProvider, index IngestionVectorIndex, logger *slog.Logger, cfg IngestionWorkerConfig) (*IngestionWorker, error) {
	if repo == nil || objects == nil || parser == nil || chunker == nil || models == nil || embeddings == nil || index == nil {
		return nil, errors.New("resource worker dependencies are incomplete")
	}
	if cfg.Concurrency == 0 {
		cfg.Concurrency = 2
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.LeaseDuration == 0 {
		cfg.LeaseDuration = 60 * time.Second
	}
	if cfg.JobTimeout == 0 {
		cfg.JobTimeout = 120 * time.Second
	}
	if cfg.Concurrency < 1 || cfg.Concurrency > 8 || cfg.PollInterval < 100*time.Millisecond || cfg.LeaseDuration < 3*time.Second || cfg.JobTimeout < time.Second || cfg.JobTimeout > 10*time.Minute {
		return nil, errors.New("invalid resource worker limits")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &IngestionWorker{repo: repo, objects: objects, parser: parser, chunker: chunker, models: models, embeddings: embeddings, index: index, logger: logger, config: cfg}, nil
}

func (w *IngestionWorker) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	owners := make([]string, w.config.Concurrency)
	for i := range owners {
		owner, err := identifier.NewUUID()
		if err != nil {
			return err
		}
		owners[i] = owner
	}
	for _, owner := range owners {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				claimed, err := w.ProcessOne(ctx, owner)
				if ctx.Err() != nil {
					return
				}
				if err != nil {
					w.logger.Error("resource worker poll failed", "error_code", "repository_unavailable")
				}
				if claimed && err == nil {
					continue
				}
				timer := time.NewTimer(w.config.PollInterval)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}()
	}
	wg.Wait()
	return nil
}

// ProcessOne keeps every durable transition fenced by owner, attempt and lease.
func (w *IngestionWorker) ProcessOne(ctx context.Context, owner string) (bool, error) {
	now := time.Now().UTC()
	work, ok, err := w.repo.ClaimIngestionJob(ctx, owner, now, now.Add(w.config.LeaseDuration))
	if err != nil || !ok {
		return ok, err
	}
	started := time.Now()
	w.inflight.Add(1)
	defer func() { w.inflight.Add(-1); w.durationMS.Add(uint64(max(0, time.Since(started).Milliseconds()))) }()
	jobCtx, cancel := context.WithTimeout(ctx, w.config.JobTimeout)
	var lost atomic.Bool
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(min(10*time.Second, w.config.LeaseDuration/3))
		defer ticker.Stop()
		for {
			select {
			case <-jobCtx.Done():
				return
			case <-ticker.C:
				now := time.Now().UTC()
				alive, renewErr := w.repo.HeartbeatIngestionJob(jobCtx, work.Lease, now, now.Add(w.config.LeaseDuration))
				if renewErr != nil || !alive {
					lost.Store(true)
					cancel()
					return
				}
			}
		}
	}()
	err = w.process(jobCtx, work)
	cancel()
	<-heartbeatDone
	if err == nil {
		w.processed.Add(1)
		return true, nil
	}
	if lost.Load() {
		w.leaseLost.Add(1)
		return true, nil
	}
	if ctx.Err() != nil {
		return true, nil
	}
	if errors.Is(err, ErrIngestionLeaseLost) {
		w.leaseLost.Add(1)
		return true, nil
	}
	code, retryable := classifyIngestionFailure(err)
	delay := time.Duration(1<<min(max(work.Lease.Attempt, 1), 8)) * 5 * time.Second
	now = time.Now().UTC()
	updated, transitionErr := w.repo.FailIngestionJob(ctx, work.Lease, code, retryable, now.Add(delay), now)
	if transitionErr != nil {
		return true, transitionErr
	}
	if !updated {
		w.leaseLost.Add(1)
		return true, nil
	}
	w.failed.Add(1)
	w.logger.Warn("resource ingestion failed", "job_id", work.Lease.JobID, "error_code", code, "retryable", retryable, "attempt", work.Lease.Attempt)
	return true, nil
}

func (w *IngestionWorker) process(ctx context.Context, work IngestionWork) error {
	if work.Job.Type == IngestionJobPurge {
		filter := ingestionPointFilter(work.Generation, work.Job.ResourceID, "")
		err := w.index.Delete(ctx, VectorDeleteRequest{Route: work.Generation.Collection, Filter: filter, Wait: true})
		if err != nil && !errors.Is(err, ErrVectorNotFound) {
			return err
		}
		if err == nil {
			count, err := w.index.CountPoints(ctx, work.Generation.Collection, filter)
			if err != nil {
				return err
			}
			if count != 0 {
				return ErrVectorUnavailable
			}
		}
		return w.complete(ctx, work.Lease, nil)
	}
	if work.Job.Type != IngestionJobIngest && work.Job.Type != IngestionJobRebuild {
		return ErrIngestionInvalid
	}
	model, batchSize, err := w.models.CurrentModel(ctx)
	if err != nil || model != work.Generation.Model || batchSize < 1 {
		return ingestionBoundaryError(ctx, ErrIngestionModelUnavailable)
	}
	chunks := work.Chunks
	if work.Job.Type == IngestionJobRebuild {
		chunks, err = w.repo.PrepareIngestionChunks(ctx, work.Lease, nil, time.Now().UTC())
		if err != nil {
			return err
		}
	}
	if len(chunks) == 0 || work.Job.Type == IngestionJobIngest {
		reader, metadata, err := w.objects.Open(ctx, work.Source)
		if err != nil {
			return err
		}
		if metadata.ByteSize != work.Metadata.ByteSize || metadata.Checksum != work.Metadata.Checksum || metadata.MIMEType != work.Metadata.MIMEType {
			reader.Close()
			return ErrObjectUnsupported
		}
		metadata.Filename = work.Metadata.Filename
		document, parseErr := w.parser.Parse(ctx, ParseInput{Reader: reader, Metadata: metadata})
		reader.Close()
		if parseErr != nil {
			return parseErr
		}
		drafts, err := w.chunker.Chunk(ctx, document, ChunkPolicy{MaxTokens: min(model.MaxTokens, 4096), OverlapTokens: min(model.MaxTokens/8, 256), MaxCharacters: 1200})
		if err != nil {
			return err
		}
		if len(drafts) < 1 || len(drafts) > 4096 {
			return ErrParseFailed
		}
		chunks, err = w.repo.PrepareIngestionChunks(ctx, work.Lease, drafts, time.Now().UTC())
		if err != nil {
			return err
		}
	}
	if len(chunks) == 0 {
		return ErrParseFailed
	}
	if err := w.index.EnsureCollection(ctx, ingestionCollectionSpec(work.Generation)); err != nil {
		return err
	}
	receipts := make([]ManifestReceipt, 0, len(chunks))
	for start := 0; start < len(chunks); {
		end, size := start, 0
		for end < len(chunks) && end-start < min(batchSize, 32) && size+len(chunks[end].Draft.Content) <= MaxDocumentEmbeddingBatchBytes {
			size += len(chunks[end].Draft.Content)
			end++
		}
		if end == start {
			return ErrIngestionInvalid
		}
		batchReceipts, err := w.writeBatch(ctx, work, chunks[start:end])
		if err != nil {
			return err
		}
		receipts = append(receipts, batchReceipts...)
		start = end
	}
	count, err := w.index.CountPoints(ctx, work.Generation.Collection, ingestionPointFilter(work.Generation, work.Job.ResourceID, work.Job.DocumentVersionID))
	if err != nil {
		return err
	}
	if count != int64(len(chunks)) {
		return ErrVectorUnavailable
	}
	return w.complete(ctx, work.Lease, receipts)
}

func (w *IngestionWorker) writeBatch(ctx context.Context, work IngestionWork, chunks []IngestionChunk) ([]ManifestReceipt, error) {
	inputs := make([]string, len(chunks))
	for i, chunk := range chunks {
		inputs[i] = chunk.Draft.Content
	}
	response, err := w.embeddings.Embed(ctx, EmbeddingRequest{Model: work.Generation.Model, Inputs: inputs})
	if err != nil {
		return nil, err
	}
	if response.Model != work.Generation.Model || len(response.Vectors) != len(chunks) {
		return nil, ErrEmbeddingFailed
	}
	points := make([]VectorPoint, len(chunks))
	ids := make([]string, len(chunks))
	receipts := make([]ManifestReceipt, len(chunks))
	for i, chunk := range chunks {
		values := response.Vectors[i]
		if len(values) != work.Generation.Model.Dimension {
			return nil, ErrEmbeddingFailed
		}
		hash, err := ingestionEmbeddingHash(values)
		if err != nil {
			return nil, err
		}
		ids[i] = chunk.ManifestID
		points[i] = VectorPoint{ID: chunk.ManifestID, Values: values, Payload: map[string]any{
			"tenant_id": work.Generation.TenantID, "knowledge_base_id": work.Generation.KnowledgeBaseID,
			"resource_id": work.Job.ResourceID, "document_version_id": work.Job.DocumentVersionID, "chunk_id": chunk.ID,
			"generation_id": work.Generation.ID, "index_generation": work.Generation.Number, "model_version_id": work.Generation.Model.ID,
			"visibility": "published", "content_sha256": chunk.ContentSHA256, "embedding_sha256": hash,
		}}
		receipts[i] = ManifestReceipt{ID: chunk.ManifestID, EmbeddingSHA256: hash}
	}
	if err := w.index.Upsert(ctx, VectorBatch{Route: work.Generation.Collection, Points: points, Wait: true}); err != nil {
		return nil, err
	}
	verified, err := w.index.GetPoints(ctx, work.Generation.Collection, ids)
	if err != nil {
		return nil, err
	}
	if len(verified) != len(points) {
		return nil, ErrVectorUnavailable
	}
	expected := make(map[string]map[string]any, len(points))
	for _, point := range points {
		expected[point.ID] = point.Payload
	}
	for _, point := range verified {
		payload, ok := expected[point.ID]
		if !ok || !ingestionPayloadMatches(point.Payload, payload) {
			return nil, ErrVectorUnavailable
		}
		delete(expected, point.ID)
	}
	return receipts, nil
}

func (w *IngestionWorker) complete(ctx context.Context, lease IngestionLease, receipts []ManifestReceipt) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ok, err := w.repo.CompleteIngestionJob(ctx, lease, receipts, time.Now().UTC())
	if err != nil {
		return err
	}
	if !ok {
		return ErrIngestionLeaseLost
	}
	return nil
}

func ingestionCollectionSpec(generation IngestionGeneration) VectorCollectionSpec {
	fields := []VectorPayloadIndex{{Field: "tenant_id", Kind: "keyword"}, {Field: "knowledge_base_id", Kind: "keyword"}, {Field: "resource_id", Kind: "keyword"}, {Field: "generation_id", Kind: "keyword"}, {Field: "visibility", Kind: "keyword"}}
	return VectorCollectionSpec{Route: generation.Collection, Dimension: generation.Model.Dimension, Distance: generation.Model.Distance, PayloadIndexes: fields}
}

func ingestionPointFilter(generation IngestionGeneration, resourceID, versionID string) map[string]any {
	must := []any{}
	for _, item := range [][2]string{{"tenant_id", generation.TenantID}, {"knowledge_base_id", generation.KnowledgeBaseID}, {"generation_id", generation.ID}, {"resource_id", resourceID}, {"document_version_id", versionID}} {
		if item[1] != "" {
			must = append(must, map[string]any{"key": item[0], "match": map[string]any{"value": item[1]}})
		}
	}
	return map[string]any{"must": must}
}

func ingestionEmbeddingHash(values []float32) (string, error) {
	h := sha256.New()
	var buffer [4]byte
	nonzero := false
	for _, value := range values {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return "", ErrEmbeddingFailed
		}
		nonzero = nonzero || value != 0
		binary.LittleEndian.PutUint32(buffer[:], math.Float32bits(value))
		h.Write(buffer[:])
	}
	if !nonzero {
		return "", ErrEmbeddingFailed
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func ingestionPayloadMatches(actual, expected map[string]any) bool {
	for key, value := range expected {
		if key == "index_generation" {
			generation, ok := value.(int64)
			if !ok || !payloadGenerationMatches(actual[key], generation) {
				return false
			}
		} else if text, ok := actual[key].(string); !ok || text != value {
			return false
		}
	}
	return true
}

func classifyIngestionFailure(err error) (string, bool) {
	var failure interface{ IngestionFailure() (string, bool) }
	if errors.As(err, &failure) {
		return failure.IngestionFailure()
	}
	switch {
	case errors.Is(err, ErrIngestionInvalid):
		return "invalid_document", false
	case errors.Is(err, ErrObjectUnsupported):
		return "source_invalid", false
	case errors.Is(err, ErrParseFailed):
		return "parse_failed", false
	case errors.Is(err, ErrIngestionModelUnavailable):
		return "model_unavailable", true
	case errors.Is(err, ErrVectorInvalid):
		return "vector_schema_mismatch", false
	case errors.Is(err, ErrEmbeddingFailed):
		return "embedding_failed", true
	case errors.Is(err, context.DeadlineExceeded):
		return "processing_timeout", true
	default:
		return "processing_unavailable", true
	}
}

func (w *IngestionWorker) Metrics() string {
	return fmt.Sprintf("# TYPE msp_resource_ingestion_completed_total counter\nmsp_resource_ingestion_completed_total %d\n# TYPE msp_resource_ingestion_failed_attempts_total counter\nmsp_resource_ingestion_failed_attempts_total %d\n# TYPE msp_resource_ingestion_lease_lost_total counter\nmsp_resource_ingestion_lease_lost_total %d\n# TYPE msp_resource_ingestion_inflight gauge\nmsp_resource_ingestion_inflight %d\n# TYPE msp_resource_ingestion_duration_seconds_total counter\nmsp_resource_ingestion_duration_seconds_total %.3f\n", w.processed.Load(), w.failed.Load(), w.leaseLost.Load(), w.inflight.Load(), float64(w.durationMS.Load())/1000)
}
