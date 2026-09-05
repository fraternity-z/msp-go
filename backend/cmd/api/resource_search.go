package main

import (
	"log/slog"

	resourceapp "mathstudy/backend/internal/application/resource"
	"mathstudy/backend/internal/platform/metrics"
)

func resourceSearchObserver(store *metrics.Store, logger *slog.Logger) resourceapp.SearchObserver {
	return resourceapp.SearchObserverFunc(func(observation resourceapp.SearchObservation) {
		store.ObserveResourceSearch(metrics.ResourceSearchObservation{
			Duration: observation.Duration, Stages: observation.Stages, Mode: observation.Mode,
			Failed: observation.Failed, Empty: observation.Empty, LexicalCandidates: observation.LexicalCandidates,
			VectorCandidates: observation.VectorCandidates, FilteredCandidates: observation.FilteredCandidates,
			References: observation.References, DegradedReasons: observation.DegradedReasons,
		})
		logger.Info("resource retrieval completed", "mode", observation.Mode, "failed", observation.Failed,
			"duration_ms", observation.Duration.Milliseconds(), "fts_candidates", observation.LexicalCandidates,
			"vector_candidates", observation.VectorCandidates, "filtered_candidates", observation.FilteredCandidates,
			"references", observation.References, "degraded_reasons", observation.DegradedReasons)
	})
}
