package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	resourceapp "mathstudy/backend/internal/application/resource"
)

type maintenanceMetrics struct {
	differences     [5]atomic.Int64
	runs            [3]atomic.Int64
	history         [2]atomic.Int64
	historyFailures atomic.Int64
	uploadsRemoved  atomic.Int64
	uploadsFailures atomic.Int64
}

func (m *maintenanceMetrics) text() string {
	if m == nil {
		m = &maintenanceMetrics{}
	}
	var out strings.Builder
	out.WriteString("# TYPE msp_resource_ingestion_reconcile_differences_total counter\n")
	for i, kind := range []string{"missing", "mismatched", "extra", "scheduled", "removed"} {
		fmt.Fprintf(&out, "msp_resource_ingestion_reconcile_differences_total{kind=%q} %d\n", kind, m.differences[i].Load())
	}
	out.WriteString("# TYPE msp_resource_ingestion_reconcile_runs_total counter\n")
	for i, result := range []string{"complete", "partial", "failed"} {
		fmt.Fprintf(&out, "msp_resource_ingestion_reconcile_runs_total{result=%q} %d\n", result, m.runs[i].Load())
	}
	out.WriteString("# TYPE msp_resource_ingestion_history_deleted_total counter\n")
	for i, kind := range []string{"jobs", "outbox"} {
		fmt.Fprintf(&out, "msp_resource_ingestion_history_deleted_total{kind=%q} %d\n", kind, m.history[i].Load())
	}
	fmt.Fprintf(&out, "# TYPE msp_resource_ingestion_history_cleanup_failures_total counter\nmsp_resource_ingestion_history_cleanup_failures_total %d\n", m.historyFailures.Load())
	fmt.Fprintf(&out, "# TYPE msp_resource_ingestion_uploads_removed_total counter\nmsp_resource_ingestion_uploads_removed_total %d\n", m.uploadsRemoved.Load())
	fmt.Fprintf(&out, "# TYPE msp_resource_ingestion_upload_cleanup_failures_total counter\nmsp_resource_ingestion_upload_cleanup_failures_total %d\n", m.uploadsFailures.Load())
	return out.String()
}

func reconcileObserved(ctx context.Context, rt runtime, g resourceapp.IngestionGeneration, apply bool, maxPages int) (resourceapp.IngestionReconcileReport, error) {
	report, err := rt.worker.Reconcile(ctx, g, apply, maxPages)
	if rt.maintenance != nil {
		for i, count := range []int{report.Missing, report.Mismatched, report.Extra, report.Scheduled, report.Removed} {
			if count > 0 {
				rt.maintenance.differences[i].Add(int64(count))
			}
		}
		result := 0
		if err != nil {
			result = 2
		} else if !report.Complete {
			result = 1
		}
		rt.maintenance.runs[result].Add(1)
	}
	return report, err
}

type uploadCleanupRepository interface {
	ClaimStaleIngestionUploads(context.Context, time.Time, int) ([]resourceapp.IngestionUploadCleanup, error)
	FinishIngestionUploadCleanup(context.Context, string, string, time.Time) (bool, error)
}

func runCleanupBatch(ctx context.Context, rt runtime) {
	if ctx.Err() != nil {
		return
	}
	result, err := rt.repo.CleanupIngestionHistory(ctx, time.Now().UTC(), 1000)
	if err != nil {
		if rt.maintenance != nil {
			rt.maintenance.historyFailures.Add(1)
		}
		slog.Warn("resource ingestion history cleanup failed", "error_code", "history_cleanup_unavailable")
	} else if rt.maintenance != nil {
		rt.maintenance.history[0].Add(result.Jobs)
		rt.maintenance.history[1].Add(result.OutboxEvents)
	}
	repo, ok := rt.repo.(uploadCleanupRepository)
	if !ok || rt.deleteObject == nil || ctx.Err() != nil {
		return
	}
	items, err := repo.ClaimStaleIngestionUploads(ctx, time.Now().UTC(), 8)
	if err != nil {
		recordUploadCleanupFailure(rt)
		return
	}
	for _, item := range items {
		if ctx.Err() != nil {
			return
		}
		if err := rt.deleteObject(ctx, item.Source); err != nil {
			recordUploadCleanupFailure(rt)
			continue
		}
		finished, err := repo.FinishIngestionUploadCleanup(ctx, item.ID, item.LeaseToken, time.Now().UTC())
		if err != nil || !finished {
			recordUploadCleanupFailure(rt)
		} else if rt.maintenance != nil {
			rt.maintenance.uploadsRemoved.Add(1)
		}
	}
}

func recordUploadCleanupFailure(rt runtime) {
	if rt.maintenance != nil {
		rt.maintenance.uploadsFailures.Add(1)
	}
	slog.Warn("resource ingestion upload cleanup failed", "error_code", "upload_cleanup_unavailable")
}
