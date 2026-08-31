package health

import (
	"context"
	"time"
)

// Pinger is implemented by dependencies that can verify connectivity.
type Pinger interface {
	Ping(context.Context) error
}

// RedisPingerFunc adapts a function into a Pinger.
type RedisPingerFunc func(context.Context) error

// Ping calls f(ctx).
func (f RedisPingerFunc) Ping(ctx context.Context) error {
	return f(ctx)
}

// Checker evaluates process and dependency health.
type Checker struct {
	version string
	db      Pinger
	redis   Pinger
	qdrant  Pinger
}

// ComponentStatus describes one health component.
type ComponentStatus struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// DetailedStatus is returned by /health/detailed.
type DetailedStatus struct {
	Status     string                     `json:"status"`
	Version    string                     `json:"version"`
	CheckedAt  time.Time                  `json:"checked_at"`
	Components map[string]ComponentStatus `json:"components"`
}

// NewChecker creates a dependency health checker.  The optional fourth
// dependency keeps existing callers source-compatible while allowing the
// vector store to be reported when it is enabled.
func NewChecker(version string, db Pinger, redis Pinger, optional ...Pinger) Checker {
	var qdrant Pinger
	if len(optional) > 0 {
		qdrant = optional[0]
	}
	return Checker{version: version, db: db, redis: redis, qdrant: qdrant}
}

// Simple returns the lightweight health payload.
func (c Checker) Simple() map[string]string {
	return map[string]string{
		"status":  "healthy",
		"version": c.version,
	}
}

// Detailed checks PostgreSQL and Redis with a short timeout.
func (c Checker) Detailed(ctx context.Context) DetailedStatus {
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	components := map[string]ComponentStatus{
		"app": {Status: "healthy"},
	}
	checks := make([]componentCheck, 0, 3)
	if c.db != nil {
		checks = append(checks, componentCheck{name: "postgres", pinger: c.db})
	}
	if c.redis != nil {
		checks = append(checks, componentCheck{name: "redis", pinger: c.redis})
	}
	if c.qdrant != nil {
		checks = append(checks, componentCheck{name: "qdrant", pinger: c.qdrant})
	}

	results := make(chan componentResult, len(checks))
	for _, check := range checks {
		go func() {
			results <- componentResult{name: check.name, status: pingComponent(checkCtx, check.pinger)}
		}()
	}
	for range checks {
		select {
		case result := <-results:
			components[result.name] = result.status
		case <-checkCtx.Done():
			for _, check := range checks {
				if _, exists := components[check.name]; !exists {
					components[check.name] = ComponentStatus{Status: "unhealthy", Error: checkCtx.Err().Error()}
				}
			}
			return detailedStatus(c.version, components)
		}
	}
	return detailedStatus(c.version, components)
}

type componentCheck struct {
	name   string
	pinger Pinger
}

type componentResult struct {
	name   string
	status ComponentStatus
}

func detailedStatus(version string, components map[string]ComponentStatus) DetailedStatus {
	status := "healthy"
	for name, component := range components {
		if name != "app" && component.Status != "healthy" {
			status = "degraded"
			break
		}
	}
	return DetailedStatus{
		Status:     status,
		Version:    version,
		CheckedAt:  time.Now().UTC(),
		Components: components,
	}
}

func pingComponent(ctx context.Context, pinger Pinger) ComponentStatus {
	if err := pinger.Ping(ctx); err != nil {
		return ComponentStatus{Status: "unhealthy", Error: err.Error()}
	}
	return ComponentStatus{Status: "healthy"}
}
