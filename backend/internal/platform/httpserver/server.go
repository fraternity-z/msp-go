package httpserver

import (
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"mathstudy/backend/internal/platform/config"
	"mathstudy/backend/internal/platform/health"
	"mathstudy/backend/internal/platform/metrics"
	"mathstudy/backend/internal/platform/middleware"
)

// RouteRegistrar attaches business routes to the shared mux.
type RouteRegistrar func(*http.ServeMux)

type handlerOptions struct {
	registrars []RouteRegistrar
}

// Option customizes the HTTP handler tree.
type Option func(*handlerOptions)

// WithRoutes registers business routes on the shared mux.
func WithRoutes(registrar RouteRegistrar) Option {
	return func(options *handlerOptions) {
		if registrar != nil {
			options.registrars = append(options.registrars, registrar)
		}
	}
}

// NewHandler builds the complete HTTP handler tree.
func NewHandler(cfg config.Config, logger *slog.Logger, checker health.Checker, store *metrics.Store, opts ...Option) (http.Handler, error) {
	options := handlerOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	managementAccess, err := newManagementAccess(cfg.ManagementAllowedCIDRs)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, checker.Simple())
	})
	mux.HandleFunc("GET /health/detailed", func(w http.ResponseWriter, r *http.Request) {
		if !managementAccess.Allow(r) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "management endpoint is restricted")
			return
		}
		status := checker.Detailed(r.Context())
		httpStatus := http.StatusOK
		if status.Status != "healthy" {
			httpStatus = http.StatusServiceUnavailable
		}
		writeJSON(w, httpStatus, status)
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		if !cfg.MetricsEnabled {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "metrics disabled")
			return
		}
		if !managementAccess.Allow(r) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "management endpoint is restricted")
			return
		}
		w.Header().Set("Content-Type", metrics.ContentType)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(store.Render()))
	})
	for _, registrar := range options.registrars {
		registrar(mux)
	}
	router := muxWithFallbacks(mux)

	return middleware.Chain(
		router,
		middleware.RequestID,
		middleware.SecurityHeaders,
		middleware.TimeoutByRequest(cfg.RequestTimeout, func(r *http.Request) time.Duration {
			return requestTimeout(cfg, r)
		}),
		middleware.RequestMetrics(store),
		middleware.CORS(cfg.CORSOrigins, cfg.CORSAllowMethods, cfg.CORSAllowHeaders),
		middleware.Gzip,
		middleware.RequestLogger(logger),
	), nil
}

func requestTimeout(cfg config.Config, r *http.Request) time.Duration {
	if r.Method == http.MethodPost && r.URL.Path == "/v1/responses" {
		return sessionChatTimeout(cfg)
	}
	if isSessionChatRequest(r, cfg.APIV1Prefix) {
		return sessionChatTimeout(cfg)
	}
	if isLongRunningAIRequest(r, cfg.APIV1Prefix) {
		return cfg.ExerciseGenTimeout
	}
	if isEmbeddingConfigRequest(r, cfg.APIV1Prefix) {
		return embeddingConfigTimeout()
	}
	return cfg.RequestTimeout
}

// Keep below the default HTTP write deadline while allowing a 300-second probe plus response time.
const embeddingConfigMaxTimeout = 5*time.Minute + 5*time.Second

func embeddingConfigTimeout() time.Duration {
	return embeddingConfigMaxTimeout
}

func isEmbeddingConfigRequest(r *http.Request, apiPrefix string) bool {
	return (r.Method == http.MethodPost && r.URL.Path == apiPrefix+"/admin/ai-config/embeddings/test") ||
		(r.Method == http.MethodPut && r.URL.Path == apiPrefix+"/admin/ai-config/embeddings/active")
}

func sessionChatTimeout(cfg config.Config) time.Duration {
	if cfg.SessionChatTimeout > 0 {
		return cfg.SessionChatTimeout
	}
	if cfg.ExerciseGenTimeout > 0 {
		return cfg.ExerciseGenTimeout
	}
	return cfg.RequestTimeout
}

func isLongRunningAIRequest(r *http.Request, apiPrefix string) bool {
	if r.Method != http.MethodPost {
		return false
	}
	switch r.URL.Path {
	case apiPrefix + "/exercise/generate",
		apiPrefix + "/daily-question/today/prepare",
		apiPrefix + "/portrait/generate":
		return true
	}
	return false
}

func isSessionChatRequest(r *http.Request, apiPrefix string) bool {
	if r.Method != http.MethodPost {
		return false
	}
	if r.URL.Path == apiPrefix+"/session/start-chat" {
		return true
	}
	sessionPath := strings.TrimPrefix(r.URL.Path, apiPrefix+"/session/")
	parts := strings.Split(sessionPath, "/")
	return len(parts) == 2 && parts[0] != "" && parts[1] == "chat"
}

func muxWithFallbacks(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		candidate, pattern := mux.Handler(r)
		if pattern != "" {
			mux.ServeHTTP(w, r)
			return
		}

		// ServeMux uses an empty pattern for both its internal 404 and 405 handlers.
		probe := newStatusProbe()
		candidate.ServeHTTP(probe, r)
		switch probe.status {
		case http.StatusNotFound:
			notFoundHandler(w, r)
		case http.StatusMethodNotAllowed:
			w.Header().Set("Allow", probe.Header().Get("Allow"))
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		default:
			mux.ServeHTTP(w, r)
		}
	})
}

type statusProbe struct {
	header http.Header
	status int
}

func newStatusProbe() *statusProbe {
	return &statusProbe{header: make(http.Header)}
}

func (p *statusProbe) Header() http.Header {
	return p.header
}

func (p *statusProbe) WriteHeader(status int) {
	if p.status == 0 {
		p.status = status
	}
}

func (p *statusProbe) Write(body []byte) (int, error) {
	if p.status == 0 {
		p.status = http.StatusOK
	}
	return len(body), nil
}

func notFoundHandler(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "API route not found")
		return
	}
	writeError(w, http.StatusNotFound, "NOT_FOUND", "route not found")
}

type managementAccess struct {
	networks []*net.IPNet
}

func newManagementAccess(cidrs []string) (managementAccess, error) {
	access := managementAccess{networks: []*net.IPNet{}}
	for _, cidr := range cidrs {
		cidr = strings.TrimSpace(cidr)
		if cidr == "" {
			continue
		}
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			return managementAccess{}, err
		}
		access.networks = append(access.networks, network)
	}
	return access, nil
}

func (a managementAccess) Allow(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return false
	}
	for _, network := range a.networks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
