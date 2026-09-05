package metrics

import (
	"fmt"
	"strings"
	"time"
)

var resourceSearchModes = [...]string{"none", "fts_only", "vector_only", "hybrid"}
var resourceSearchStages = [...]string{"total", "scope", "fts", "vector", "authorize", "rerank", "neighbors"}
var resourceSearchReasons = [...]string{"fts_unavailable", "vector_unavailable", "rerank_unavailable", "neighbors_unavailable", "other"}

// ResourceSearchObservation deliberately has no query, identity or document fields.
type ResourceSearchObservation struct {
	Duration           time.Duration
	Stages             map[string]time.Duration
	Mode               string
	Failed             bool
	Empty              bool
	LexicalCandidates  int
	VectorCandidates   int
	FilteredCandidates int
	References         int
	DegradedReasons    []string
}

type resourceSearchHistogram struct {
	count   uint64
	sum     float64
	buckets [len(httpDurationBuckets)]uint64
}

type resourceSearchStats struct {
	requests                                     [len(resourceSearchModes)][2]uint64
	durations                                    [len(resourceSearchStages)]resourceSearchHistogram
	reasons                                      [len(resourceSearchReasons)]uint64
	lexical, vector, filtered, references, empty uint64
}

func (s *Store) ObserveResourceSearch(observation ResourceSearchObservation) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	mode, failed := 0, 0
	for i, value := range resourceSearchModes {
		if observation.Mode == value {
			mode = i
			break
		}
	}
	if observation.Failed {
		failed = 1
	}
	s.resourceSearch.requests[mode][failed]++
	if observation.Empty && !observation.Failed {
		s.resourceSearch.empty++
	}
	s.resourceSearch.lexical += uint64(max(0, observation.LexicalCandidates))
	s.resourceSearch.vector += uint64(max(0, observation.VectorCandidates))
	s.resourceSearch.filtered += uint64(max(0, observation.FilteredCandidates))
	s.resourceSearch.references += uint64(max(0, observation.References))
	for i, stage := range resourceSearchStages {
		duration, exists := observation.Stages[stage]
		if i == 0 {
			duration, exists = observation.Duration, true
		}
		if !exists {
			continue
		}
		series := &s.resourceSearch.durations[i]
		seconds := nonNegativeSeconds(duration)
		series.count++
		series.sum += seconds
		for j, bound := range httpDurationBuckets {
			if seconds <= bound {
				series.buckets[j]++
			}
		}
	}
	seen := [len(resourceSearchReasons)]bool{}
	for _, reason := range observation.DegradedReasons {
		index := len(resourceSearchReasons) - 1
		for i, value := range resourceSearchReasons {
			if reason == value {
				index = i
				break
			}
		}
		if !seen[index] {
			s.resourceSearch.reasons[index]++
			seen[index] = true
		}
	}
}

func (s *Store) renderResourceSearchMetrics(b *strings.Builder) {
	s.mu.RLock()
	stats := s.resourceSearch
	s.mu.RUnlock()
	b.WriteString("# HELP msp_resource_search_requests_total Resource retrieval requests by bounded mode and outcome.\n# TYPE msp_resource_search_requests_total counter\n")
	for i, mode := range resourceSearchModes {
		for failed, outcome := range [...]string{"success", "error"} {
			fmt.Fprintf(b, "msp_resource_search_requests_total{mode=%q,outcome=%q} %d\n", mode, outcome, stats.requests[i][failed])
		}
	}
	b.WriteString("# HELP msp_resource_search_duration_seconds Resource retrieval stage duration.\n# TYPE msp_resource_search_duration_seconds histogram\n")
	for i, stage := range resourceSearchStages {
		series := stats.durations[i]
		for j, bound := range httpDurationBuckets {
			fmt.Fprintf(b, "msp_resource_search_duration_seconds_bucket{stage=%q,le=%q} %d\n", stage, formatFloat(bound), series.buckets[j])
		}
		fmt.Fprintf(b, "msp_resource_search_duration_seconds_bucket{stage=%q,le=\"+Inf\"} %d\n", stage, series.count)
		fmt.Fprintf(b, "msp_resource_search_duration_seconds_sum{stage=%q} %s\n", stage, formatFloat(series.sum))
		fmt.Fprintf(b, "msp_resource_search_duration_seconds_count{stage=%q} %d\n", stage, series.count)
	}
	b.WriteString("# HELP msp_resource_search_candidates_total Recalled candidates before final authorization.\n# TYPE msp_resource_search_candidates_total counter\n")
	fmt.Fprintf(b, "msp_resource_search_candidates_total{source=\"fts\"} %d\nmsp_resource_search_candidates_total{source=\"vector\"} %d\n", stats.lexical, stats.vector)
	for _, item := range []struct {
		name, help string
		value      uint64
	}{
		{"filtered", "Candidates removed by PostgreSQL authorization.", stats.filtered},
		{"references", "Authorized references returned by retrieval.", stats.references},
		{"empty", "Successful retrieval requests without results.", stats.empty},
	} {
		name := "msp_resource_search_" + item.name + "_total"
		fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", name, item.help, name, name, item.value)
	}
	b.WriteString("# HELP msp_resource_search_degradations_total Retrieval degradations by bounded reason.\n# TYPE msp_resource_search_degradations_total counter\n")
	for i, reason := range resourceSearchReasons {
		fmt.Fprintf(b, "msp_resource_search_degradations_total{reason=%q} %d\n", reason, stats.reasons[i])
	}
}
