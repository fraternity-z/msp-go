package resource

import "sort"

type searchCandidateKey struct {
	chunkID    string
	resourceID string
	versionID  string
	generation int64
}

func searchKey(candidate SearchCandidate) searchCandidateKey {
	return searchCandidateKey{candidate.ChunkID, candidate.ResourceID, candidate.DocumentVersionID, candidate.Generation}
}

type fusedSearchCandidate struct {
	candidate   SearchCandidate
	score       float64
	lexicalRank int
	vectorRank  int
	adjacent    bool
}

func (candidate fusedSearchCandidate) sources() []string {
	sources := make([]string, 0, 2)
	if candidate.lexicalRank > 0 {
		sources = append(sources, "fts")
	}
	if candidate.vectorRank > 0 {
		sources = append(sources, "vector")
	}
	if candidate.adjacent {
		sources = append(sources, "adjacent")
	}
	return sources
}

// Original ranks are retained internally. Duplicate entries within one source
// contribute once; provider scores never affect cross-source ordering.
func fuseSearchCandidates(lexical, vector []SearchCandidate, generation int64) []fusedSearchCandidate {
	items := make(map[searchCandidateKey]*fusedSearchCandidate)
	for source, candidates := range [][]SearchCandidate{lexical, vector} {
		seen := make(map[searchCandidateKey]bool)
		for index, candidate := range candidates {
			if index >= maxSearchCandidates {
				break
			}
			if candidate.Generation != generation || !isSearchUUID(candidate.ChunkID) ||
				!isSearchUUID(candidate.ResourceID) || !isSearchUUID(candidate.DocumentVersionID) {
				continue
			}
			key := searchKey(candidate)
			if seen[key] {
				continue
			}
			seen[key] = true
			item, ok := items[key]
			if !ok {
				candidate.Score = 0
				item = &fusedSearchCandidate{candidate: candidate}
				items[key] = item
			}
			rank := index + 1
			item.score += 1 / float64(60+rank)
			if source == 0 {
				item.lexicalRank = rank
			} else {
				item.vectorRank = rank
			}
		}
	}
	result := make([]fusedSearchCandidate, 0, len(items))
	for _, item := range items {
		result = append(result, *item)
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := result[i], result[j]
		if left.score != right.score {
			return left.score > right.score
		}
		if left.candidate.ChunkID != right.candidate.ChunkID {
			return left.candidate.ChunkID < right.candidate.ChunkID
		}
		if left.candidate.ResourceID != right.candidate.ResourceID {
			return left.candidate.ResourceID < right.candidate.ResourceID
		}
		return left.candidate.DocumentVersionID < right.candidate.DocumentVersionID
	})
	if len(result) > maxSearchCandidates {
		result = result[:maxSearchCandidates]
	}
	return result
}
