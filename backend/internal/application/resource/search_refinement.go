package resource

import (
	"context"
	"errors"
	"time"
)

type SearchOption func(*SearchService)

func WithSearchReranker(reranker SearchReranker) SearchOption {
	return func(service *SearchService) { service.reranker = reranker }
}

func WithSearchObserver(observer SearchObserver) SearchOption {
	return func(service *SearchService) { service.observer = observer }
}

// SearchObservation contains bounded operational data, never queries or source text.
type SearchObservation struct {
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

type SearchObserver interface{ ObserveSearch(SearchObservation) }
type SearchObserverFunc func(SearchObservation)

func (f SearchObserverFunc) ObserveSearch(observation SearchObservation) { f(observation) }

type SearchNeighborRepository interface {
	SearchNeighbors(context.Context, SearchScope, []SearchCandidate, int) ([]SearchCandidate, error)
}

func (s *SearchService) refineSearch(ctx context.Context, scope SearchScope, request SearchRequest, fused []fusedSearchCandidate, authorized []AuthorizedSearchChunk, response *SearchResponse, observation *SearchObservation) ([]fusedSearchCandidate, error) {
	if s.reranker != nil && len(fused) > 1 {
		start := time.Now()
		chunks := searchChunkMap(authorized)
		batch := make([]AuthorizedSearchChunk, 0, 40)
		remaining := 64 << 10
		for _, item := range fused {
			chunk := chunks[searchKey(item.candidate)]
			if searchChunkBytes(chunk) > remaining || len(batch) == 40 {
				break
			}
			remaining -= searchChunkBytes(chunk)
			batch = append(batch, chunk)
		}
		if len(batch) > 1 {
			deadline, _ := ctx.Deadline()
			timeout := min(2*time.Second, time.Until(deadline)/2)
			rerankCtx, cancel := context.WithTimeout(ctx, timeout)
			order, err := s.reranker.Rerank(rerankCtx, request.Query, batch)
			cancel()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if err == nil && validRerankOrder(batch, order) {
				byKey := make(map[searchCandidateKey]fusedSearchCandidate, len(fused))
				for _, item := range fused {
					byKey[searchKey(item.candidate)] = item
				}
				for i, candidate := range order {
					fused[i] = byKey[searchKey(candidate)]
				}
			} else if !errors.Is(err, ErrRerankDisabled) {
				addSearchDegradation(response, "rerank_unavailable")
			}
		}
		observation.Stages["rerank"] = time.Since(start)
	}
	neighbors, ok := s.repo.(SearchNeighborRepository)
	if !ok || len(fused) == 0 {
		return fused, nil
	}
	start := time.Now()
	seeds := make([]SearchCandidate, min(request.TopK, len(fused)))
	for i := range seeds {
		seeds[i] = fused[i].candidate
	}
	neighborTimeout := 500 * time.Millisecond
	if deadline, ok := ctx.Deadline(); ok {
		neighborTimeout = min(neighborTimeout, time.Until(deadline)/2)
	}
	neighborCtx, cancelNeighbors := context.WithTimeout(ctx, neighborTimeout)
	adjacent, err := neighbors.SearchNeighbors(neighborCtx, scope, seeds, 2)
	if err == nil {
		err = neighborCtx.Err()
	}
	cancelNeighbors()
	observation.Stages["neighbors"] = time.Since(start)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		addSearchDegradation(response, "neighbors_unavailable")
		return fused, nil
	}
	seen := make(map[searchCandidateKey]bool, len(fused))
	for _, item := range fused {
		seen[searchKey(item.candidate)] = true
	}
	// Neighbors follow the primary hits, so expansion cannot displace a better
	// primary match. Final authorization below also checks every added identity.
	for _, candidate := range adjacent {
		if len(fused) == maxSearchCandidates {
			break
		}
		key := searchKey(candidate)
		if seen[key] || candidate.Generation != scope.Generation {
			continue
		}
		seen[key] = true
		fused = append(fused, fusedSearchCandidate{candidate: candidate, adjacent: true})
	}
	return fused, nil
}

func validRerankOrder(chunks []AuthorizedSearchChunk, order []SearchCandidate) bool {
	if len(chunks) != len(order) {
		return false
	}
	wanted := make(map[searchCandidateKey]bool, len(chunks))
	for _, chunk := range chunks {
		wanted[searchKey(chunk.Candidate)] = true
	}
	for _, candidate := range order {
		key := searchKey(candidate)
		if !wanted[key] {
			return false
		}
		delete(wanted, key)
	}
	return len(wanted) == 0
}

func authorizedSearchOrder(fused []fusedSearchCandidate, chunks []AuthorizedSearchChunk) []fusedSearchCandidate {
	allowed := searchChunkMap(chunks)
	result := make([]fusedSearchCandidate, 0, len(fused))
	for _, item := range fused {
		if _, ok := allowed[searchKey(item.candidate)]; ok {
			result = append(result, item)
		}
	}
	return result
}

func searchChunkMap(chunks []AuthorizedSearchChunk) map[searchCandidateKey]AuthorizedSearchChunk {
	result := make(map[searchCandidateKey]AuthorizedSearchChunk, len(chunks))
	for _, chunk := range chunks {
		result[searchKey(chunk.Candidate)] = chunk
	}
	return result
}

func searchChunkBytes(chunk AuthorizedSearchChunk) int {
	size := len(chunk.Content) + len(chunk.Title) + len(chunk.QuoteHash)
	if chunk.SectionPath != nil {
		size += len(*chunk.SectionPath)
	}
	return size
}

func searchHit(scope SearchScope, chunk AuthorizedSearchChunk, score float64, sources []string) SearchHit {
	return SearchHit{Content: chunk.Content, Score: score, Sources: sources, Citation: SearchCitation{
		KnowledgeBaseID: scope.KnowledgeBaseID, ResourceID: chunk.Candidate.ResourceID,
		DocumentVersionID: chunk.Candidate.DocumentVersionID, ChunkID: chunk.Candidate.ChunkID,
		Generation: chunk.Candidate.Generation, Title: chunk.Title, Page: chunk.Page,
		SectionPath: chunk.SectionPath, QuoteHash: chunk.QuoteHash,
	}}
}

func addSearchDegradation(response *SearchResponse, reason string) {
	response.Degraded = true
	for _, value := range response.DegradedReasons {
		if value == reason {
			return
		}
	}
	response.DegradedReasons = append(response.DegradedReasons, reason)
}
