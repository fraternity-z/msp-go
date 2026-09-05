package metrics

import (
	"fmt"
	"math"
	"runtime"
	runtimemetrics "runtime/metrics"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const ContentType = "text/plain; version=0.0.4; charset=utf-8"

var httpDurationBuckets = [...]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// RuntimeStatsProvider returns point-in-time dependency pool statistics.
type RuntimeStatsProvider func() RuntimeStats

// RuntimeStats contains dependency statistics rendered with process metrics.
type RuntimeStats struct {
	Postgres PostgresPoolStats
	Redis    RedisPoolStats
}

// PostgresPoolStats is a dependency-neutral snapshot of pgx pool statistics.
type PostgresPoolStats struct {
	MaxConnections          int64
	TotalConnections        int64
	AcquiredConnections     int64
	IdleConnections         int64
	ConstructingConnections int64
	AcquireCount            int64
	EmptyAcquireCount       int64
	CanceledAcquireCount    int64
	AcquireDuration         time.Duration
	EmptyAcquireWaitTime    time.Duration
}

// RedisPoolStats is a dependency-neutral snapshot of go-redis pool statistics.
type RedisPoolStats struct {
	MaxConnections   int64
	TotalConnections int64
	IdleConnections  int64
	StaleConnections int64
	Hits             uint64
	Misses           uint64
	Timeouts         uint64
	WaitCount        uint64
	Unusable         uint64
	WaitDuration     time.Duration
}

// HTTPStats summarizes bounded request metrics for the operations dashboard.
type HTTPStats struct {
	RequestsTotal     uint64
	ClientErrorsTotal uint64
	ServerErrorsTotal uint64
	AverageDuration   time.Duration
	P95Duration       time.Duration
	P95Clamped        bool
}

// ProcessStats contains portable Go process and runtime measurements.
type ProcessStats struct {
	CPUUsagePercent   float64
	HeapUsedBytes     uint64
	HeapReservedBytes uint64
	Goroutines        int
	LogicalCPUs       int
	GOMAXPROCS        int
	GoVersion         string
	OS                string
	Arch              string
}

// OperationalSnapshot is a point-in-time view used by authenticated admin APIs.
type OperationalSnapshot struct {
	Version          string
	Environment      string
	StartedAt        time.Time
	Uptime           time.Duration
	TrafficStartedAt time.Time
	TrafficWindow    time.Duration
	HTTP             HTTPStats
	Process          ProcessStats
	Dependencies     RuntimeStats
}

type httpLabels struct {
	method      string
	route       string
	statusClass string
}

type httpSeries struct {
	count       uint64
	durationSum float64
	buckets     []uint64
}

type httpSnapshot struct {
	labels      httpLabels
	count       uint64
	durationSum float64
	buckets     []uint64
}

// Store keeps Prometheus-compatible process and request metrics.
type Store struct {
	version               string
	environment           string
	startedAt             time.Time
	requests              atomic.Uint64
	responsesInputTokens  atomic.Uint64
	responsesOutputTokens atomic.Uint64

	mu                   sync.RWMutex
	http                 map[httpLabels]*httpSeries
	operationalHTTP      map[httpLabels]*httpSeries
	operationalStartedAt time.Time
	runtimeStatsProvider RuntimeStatsProvider
	resourceSearch       resourceSearchStats
}

// NewStore creates a metrics store for the API process.
func NewStore(version, environment string) *Store {
	startedAt := time.Now().UTC()
	return &Store{
		version:              version,
		environment:          environment,
		startedAt:            startedAt,
		http:                 make(map[httpLabels]*httpSeries),
		operationalHTTP:      make(map[httpLabels]*httpSeries),
		operationalStartedAt: startedAt,
	}
}

// IncRequests increments the legacy process-level HTTP request counter.
func (s *Store) IncRequests() {
	s.requests.Add(1)
}

// ObserveOpenAIResponsesUsage records provider-reported token usage without high-cardinality labels.
func (s *Store) ObserveOpenAIResponsesUsage(inputTokens, outputTokens int) {
	if s == nil {
		return
	}
	if inputTokens > 0 {
		s.responsesInputTokens.Add(uint64(inputTokens))
	}
	if outputTokens > 0 {
		s.responsesOutputTokens.Add(uint64(outputTokens))
	}
}

// ObserveHTTPRequest records one completed HTTP request using bounded labels.
func (s *Store) ObserveHTTPRequest(method, route string, status int, duration time.Duration) {
	s.requests.Add(1)
	labels := httpLabels{
		method:      normalizeMethod(method),
		route:       normalizeRoute(route),
		statusClass: statusCodeClass(status),
	}
	durationSeconds := nonNegativeSeconds(duration)

	s.mu.Lock()
	observeHTTPSeries(s.http, labels, durationSeconds)
	observeHTTPSeries(s.operationalHTTP, labels, durationSeconds)
	s.mu.Unlock()
}

// ResetOperationalHTTP starts a new traffic-analysis window for the admin
// operations dashboard without changing process-lifetime Prometheus counters.
func (s *Store) ResetOperationalHTTP() time.Time {
	resetAt := time.Now().UTC()
	s.mu.Lock()
	s.operationalHTTP = make(map[httpLabels]*httpSeries)
	s.operationalStartedAt = resetAt
	s.mu.Unlock()
	return resetAt
}

func observeHTTPSeries(target map[httpLabels]*httpSeries, labels httpLabels, durationSeconds float64) {
	series := target[labels]
	if series == nil {
		series = &httpSeries{buckets: make([]uint64, len(httpDurationBuckets))}
		target[labels] = series
	}
	series.count++
	series.durationSum += durationSeconds
	for i, upperBound := range httpDurationBuckets {
		if durationSeconds <= upperBound {
			series.buckets[i]++
		}
	}
}

// SetRuntimeStatsProvider configures dependency pool metrics sampled at scrape time.
func (s *Store) SetRuntimeStatsProvider(provider RuntimeStatsProvider) {
	s.mu.Lock()
	s.runtimeStatsProvider = provider
	s.mu.Unlock()
}

// OperationalSnapshot returns request, process, and dependency metrics without
// exposing Prometheus text parsing to application code.
func (s *Store) OperationalSnapshot() OperationalSnapshot {
	httpSnapshots, trafficStartedAt, runtimeProvider := s.operationalSnapshot()
	dependencies := RuntimeStats{}
	if runtimeProvider != nil {
		dependencies = runtimeProvider()
	}

	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	now := time.Now().UTC()
	uptime := now.Sub(s.startedAt)
	if uptime < 0 {
		uptime = 0
	}
	trafficWindow := now.Sub(trafficStartedAt)
	if trafficWindow < 0 {
		trafficWindow = 0
	}

	return OperationalSnapshot{
		Version:          s.version,
		Environment:      s.environment,
		StartedAt:        s.startedAt,
		Uptime:           uptime,
		TrafficStartedAt: trafficStartedAt,
		TrafficWindow:    trafficWindow,
		HTTP:             summarizeHTTP(httpSnapshots),
		Dependencies:     dependencies,
		Process: ProcessStats{
			CPUUsagePercent:   goCPUUsagePercent(),
			HeapUsedBytes:     memory.HeapAlloc,
			HeapReservedBytes: memory.HeapSys,
			Goroutines:        runtime.NumGoroutine(),
			LogicalCPUs:       runtime.NumCPU(),
			GOMAXPROCS:        runtime.GOMAXPROCS(0),
			GoVersion:         runtime.Version(),
			OS:                runtime.GOOS,
			Arch:              runtime.GOARCH,
		},
	}
}

// Render returns Prometheus text exposition format.
func (s *Store) Render() string {
	httpStats, runtimeProvider := s.snapshot()

	var b strings.Builder
	b.WriteString("# HELP msp_app_info MathStudyPlatform Go API build information.\n")
	b.WriteString("# TYPE msp_app_info gauge\n")
	fmt.Fprintf(&b, "msp_app_info{version=%q,environment=%q} 1\n", s.version, s.environment)
	b.WriteString("# HELP msp_http_requests_total Total HTTP requests handled by the Go API.\n")
	b.WriteString("# TYPE msp_http_requests_total counter\n")
	fmt.Fprintf(&b, "msp_http_requests_total %d\n", s.requests.Load())
	b.WriteString("# HELP msp_openai_responses_input_tokens_total Provider-reported input tokens for terminal Responses API requests.\n")
	b.WriteString("# TYPE msp_openai_responses_input_tokens_total counter\n")
	fmt.Fprintf(&b, "msp_openai_responses_input_tokens_total %d\n", s.responsesInputTokens.Load())
	b.WriteString("# HELP msp_openai_responses_output_tokens_total Provider-reported output tokens for terminal Responses API requests.\n")
	b.WriteString("# TYPE msp_openai_responses_output_tokens_total counter\n")
	fmt.Fprintf(&b, "msp_openai_responses_output_tokens_total %d\n", s.responsesOutputTokens.Load())
	renderHTTPMetrics(&b, httpStats)
	s.renderResourceSearchMetrics(&b)
	if runtimeProvider != nil {
		renderRuntimeMetrics(&b, runtimeProvider())
	}
	b.WriteString("# HELP msp_health_status Static process health marker for the Go API.\n")
	b.WriteString("# TYPE msp_health_status gauge\n")
	b.WriteString("msp_health_status{component=\"app\"} 1\n")
	return b.String()
}

func (s *Store) snapshot() ([]httpSnapshot, RuntimeStatsProvider) {
	s.mu.RLock()
	snapshots := cloneHTTPSnapshots(s.http)
	runtimeProvider := s.runtimeStatsProvider
	s.mu.RUnlock()

	sortHTTPSnapshots(snapshots)
	return snapshots, runtimeProvider
}

func (s *Store) operationalSnapshot() ([]httpSnapshot, time.Time, RuntimeStatsProvider) {
	s.mu.RLock()
	snapshots := cloneHTTPSnapshots(s.operationalHTTP)
	startedAt := s.operationalStartedAt
	runtimeProvider := s.runtimeStatsProvider
	s.mu.RUnlock()

	sortHTTPSnapshots(snapshots)
	return snapshots, startedAt, runtimeProvider
}

func cloneHTTPSnapshots(source map[httpLabels]*httpSeries) []httpSnapshot {
	snapshots := make([]httpSnapshot, 0, len(source))
	for labels, series := range source {
		snapshots = append(snapshots, httpSnapshot{
			labels:      labels,
			count:       series.count,
			durationSum: series.durationSum,
			buckets:     append([]uint64(nil), series.buckets...),
		})
	}
	return snapshots
}

func sortHTTPSnapshots(snapshots []httpSnapshot) {
	sort.Slice(snapshots, func(i, j int) bool {
		if snapshots[i].labels.method != snapshots[j].labels.method {
			return snapshots[i].labels.method < snapshots[j].labels.method
		}
		if snapshots[i].labels.route != snapshots[j].labels.route {
			return snapshots[i].labels.route < snapshots[j].labels.route
		}
		return snapshots[i].labels.statusClass < snapshots[j].labels.statusClass
	})
}

func renderHTTPMetrics(b *strings.Builder, snapshots []httpSnapshot) {
	b.WriteString("# HELP msp_http_server_requests_total HTTP requests grouped by method, route template, and status class.\n")
	b.WriteString("# TYPE msp_http_server_requests_total counter\n")
	for _, snapshot := range snapshots {
		fmt.Fprintf(
			b,
			"msp_http_server_requests_total{method=%q,route=%q,status_class=%q} %d\n",
			snapshot.labels.method,
			snapshot.labels.route,
			snapshot.labels.statusClass,
			snapshot.count,
		)
	}

	b.WriteString("# HELP msp_http_server_request_duration_seconds HTTP request duration grouped by method, route template, and status class.\n")
	b.WriteString("# TYPE msp_http_server_request_duration_seconds histogram\n")
	for _, snapshot := range snapshots {
		for i, upperBound := range httpDurationBuckets {
			fmt.Fprintf(
				b,
				"msp_http_server_request_duration_seconds_bucket{method=%q,route=%q,status_class=%q,le=%q} %d\n",
				snapshot.labels.method,
				snapshot.labels.route,
				snapshot.labels.statusClass,
				strconv.FormatFloat(upperBound, 'g', -1, 64),
				snapshot.buckets[i],
			)
		}
		fmt.Fprintf(
			b,
			"msp_http_server_request_duration_seconds_bucket{method=%q,route=%q,status_class=%q,le=\"+Inf\"} %d\n",
			snapshot.labels.method,
			snapshot.labels.route,
			snapshot.labels.statusClass,
			snapshot.count,
		)
		fmt.Fprintf(
			b,
			"msp_http_server_request_duration_seconds_sum{method=%q,route=%q,status_class=%q} %s\n",
			snapshot.labels.method,
			snapshot.labels.route,
			snapshot.labels.statusClass,
			formatFloat(snapshot.durationSum),
		)
		fmt.Fprintf(
			b,
			"msp_http_server_request_duration_seconds_count{method=%q,route=%q,status_class=%q} %d\n",
			snapshot.labels.method,
			snapshot.labels.route,
			snapshot.labels.statusClass,
			snapshot.count,
		)
	}
}

func renderRuntimeMetrics(b *strings.Builder, stats RuntimeStats) {
	b.WriteString("# HELP msp_postgres_pool_max_connections Configured PostgreSQL pool connection limit.\n")
	b.WriteString("# TYPE msp_postgres_pool_max_connections gauge\n")
	fmt.Fprintf(b, "msp_postgres_pool_max_connections %d\n", stats.Postgres.MaxConnections)
	b.WriteString("# HELP msp_postgres_pool_connections Current PostgreSQL pool connections by state.\n")
	b.WriteString("# TYPE msp_postgres_pool_connections gauge\n")
	fmt.Fprintf(b, "msp_postgres_pool_connections{state=\"total\"} %d\n", stats.Postgres.TotalConnections)
	fmt.Fprintf(b, "msp_postgres_pool_connections{state=\"acquired\"} %d\n", stats.Postgres.AcquiredConnections)
	fmt.Fprintf(b, "msp_postgres_pool_connections{state=\"idle\"} %d\n", stats.Postgres.IdleConnections)
	fmt.Fprintf(b, "msp_postgres_pool_connections{state=\"constructing\"} %d\n", stats.Postgres.ConstructingConnections)
	b.WriteString("# HELP msp_postgres_pool_acquires_total Total successful PostgreSQL pool acquires.\n")
	b.WriteString("# TYPE msp_postgres_pool_acquires_total counter\n")
	fmt.Fprintf(b, "msp_postgres_pool_acquires_total %d\n", stats.Postgres.AcquireCount)
	b.WriteString("# HELP msp_postgres_pool_empty_acquires_total PostgreSQL acquires that waited for a connection.\n")
	b.WriteString("# TYPE msp_postgres_pool_empty_acquires_total counter\n")
	fmt.Fprintf(b, "msp_postgres_pool_empty_acquires_total %d\n", stats.Postgres.EmptyAcquireCount)
	b.WriteString("# HELP msp_postgres_pool_canceled_acquires_total PostgreSQL pool acquires canceled by context.\n")
	b.WriteString("# TYPE msp_postgres_pool_canceled_acquires_total counter\n")
	fmt.Fprintf(b, "msp_postgres_pool_canceled_acquires_total %d\n", stats.Postgres.CanceledAcquireCount)
	b.WriteString("# HELP msp_postgres_pool_acquire_duration_seconds_total Total time spent acquiring PostgreSQL connections.\n")
	b.WriteString("# TYPE msp_postgres_pool_acquire_duration_seconds_total counter\n")
	fmt.Fprintf(b, "msp_postgres_pool_acquire_duration_seconds_total %s\n", formatFloat(nonNegativeSeconds(stats.Postgres.AcquireDuration)))
	b.WriteString("# HELP msp_postgres_pool_empty_acquire_wait_seconds_total Total time waiting when the PostgreSQL pool was empty.\n")
	b.WriteString("# TYPE msp_postgres_pool_empty_acquire_wait_seconds_total counter\n")
	fmt.Fprintf(b, "msp_postgres_pool_empty_acquire_wait_seconds_total %s\n", formatFloat(nonNegativeSeconds(stats.Postgres.EmptyAcquireWaitTime)))

	b.WriteString("# HELP msp_redis_pool_max_connections Configured Redis pool connection limit.\n")
	b.WriteString("# TYPE msp_redis_pool_max_connections gauge\n")
	fmt.Fprintf(b, "msp_redis_pool_max_connections %d\n", stats.Redis.MaxConnections)
	b.WriteString("# HELP msp_redis_pool_connections Current Redis pool connections by state.\n")
	b.WriteString("# TYPE msp_redis_pool_connections gauge\n")
	fmt.Fprintf(b, "msp_redis_pool_connections{state=\"total\"} %d\n", stats.Redis.TotalConnections)
	fmt.Fprintf(b, "msp_redis_pool_connections{state=\"idle\"} %d\n", stats.Redis.IdleConnections)
	fmt.Fprintf(b, "msp_redis_pool_connections{state=\"stale\"} %d\n", stats.Redis.StaleConnections)
	renderRedisCounter(b, "hits", "Redis pool hits.", stats.Redis.Hits)
	renderRedisCounter(b, "misses", "Redis pool misses.", stats.Redis.Misses)
	renderRedisCounter(b, "timeouts", "Redis pool wait timeouts.", stats.Redis.Timeouts)
	renderRedisCounter(b, "waits", "Redis pool connection waits.", stats.Redis.WaitCount)
	renderRedisCounter(b, "unusable_connections", "Redis pool unusable connections.", stats.Redis.Unusable)
	b.WriteString("# HELP msp_redis_pool_wait_duration_seconds_total Total time waiting for Redis pool connections.\n")
	b.WriteString("# TYPE msp_redis_pool_wait_duration_seconds_total counter\n")
	fmt.Fprintf(b, "msp_redis_pool_wait_duration_seconds_total %s\n", formatFloat(nonNegativeSeconds(stats.Redis.WaitDuration)))
}

func summarizeHTTP(snapshots []httpSnapshot) HTTPStats {
	stats := HTTPStats{}
	buckets := make([]uint64, len(httpDurationBuckets))
	var durationSeconds float64
	for _, snapshot := range snapshots {
		stats.RequestsTotal += snapshot.count
		durationSeconds += snapshot.durationSum
		for i, count := range snapshot.buckets {
			buckets[i] += count
		}
		switch snapshot.labels.statusClass {
		case "4xx":
			stats.ClientErrorsTotal += snapshot.count
		case "5xx":
			stats.ServerErrorsTotal += snapshot.count
		}
	}
	if stats.RequestsTotal == 0 {
		return stats
	}

	stats.AverageDuration = time.Duration(durationSeconds / float64(stats.RequestsTotal) * float64(time.Second))
	target := uint64(math.Ceil(float64(stats.RequestsTotal) * 0.95))
	for i, count := range buckets {
		if count >= target {
			stats.P95Duration = time.Duration(httpDurationBuckets[i] * float64(time.Second))
			return stats
		}
	}
	stats.P95Duration = time.Duration(httpDurationBuckets[len(httpDurationBuckets)-1] * float64(time.Second))
	stats.P95Clamped = true
	return stats
}

func goCPUUsagePercent() float64 {
	samples := []runtimemetrics.Sample{
		{Name: "/cpu/classes/total:cpu-seconds"},
		{Name: "/cpu/classes/idle:cpu-seconds"},
	}
	runtimemetrics.Read(samples)
	if samples[0].Value.Kind() != runtimemetrics.KindFloat64 || samples[1].Value.Kind() != runtimemetrics.KindFloat64 {
		return 0
	}
	total := samples[0].Value.Float64()
	idle := samples[1].Value.Float64()
	if total <= 0 {
		return 0
	}
	usage := (total - idle) / total * 100
	return math.Max(0, math.Min(100, usage))
}

func renderRedisCounter(b *strings.Builder, name, help string, value uint64) {
	metricName := "msp_redis_pool_" + name + "_total"
	fmt.Fprintf(b, "# HELP %s %s\n", metricName, help)
	fmt.Fprintf(b, "# TYPE %s counter\n", metricName)
	fmt.Fprintf(b, "%s %d\n", metricName, value)
}

func normalizeMethod(method string) string {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		return "UNKNOWN"
	}
	return method
}

func normalizeRoute(route string) string {
	route = strings.TrimSpace(route)
	if route == "" {
		return "<unmatched>"
	}
	return route
}

func statusCodeClass(status int) string {
	switch {
	case status >= 100 && status <= 199:
		return "1xx"
	case status >= 200 && status <= 299:
		return "2xx"
	case status >= 300 && status <= 399:
		return "3xx"
	case status >= 400 && status <= 499:
		return "4xx"
	case status >= 500 && status <= 599:
		return "5xx"
	default:
		return "unknown"
	}
}

func nonNegativeSeconds(duration time.Duration) float64 {
	if duration < 0 {
		return 0
	}
	return duration.Seconds()
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}
