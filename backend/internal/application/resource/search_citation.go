package resource

import (
	"context"
	"strings"
	"time"
)

type CitationRequest struct {
	KnowledgeBaseID   string
	ChunkID           string
	DocumentVersionID string
	Generation        int64
}

type SearchCitationRepository interface {
	GetSearchCitation(context.Context, SearchScope, CitationRequest) (AuthorizedSearchChunk, bool, error)
}

func (s *SearchService) GetCitation(ctx context.Context, userID string, request CitationRequest) (SearchHit, error) {
	if err := ctx.Err(); err != nil {
		return SearchHit{}, err
	}
	userID, normalized, err := normalizeSearchRequest(userID, SearchRequest{Query: "citation", KnowledgeBaseID: request.KnowledgeBaseID})
	request.ChunkID = strings.ToLower(strings.TrimSpace(request.ChunkID))
	request.DocumentVersionID = strings.ToLower(strings.TrimSpace(request.DocumentVersionID))
	request.KnowledgeBaseID = normalized.KnowledgeBaseID
	if err != nil || !isSearchUUID(request.ChunkID) || !isSearchUUID(request.DocumentVersionID) || request.Generation < 1 {
		return SearchHit{}, ErrSearchInvalid
	}
	repo, ok := s.repo.(SearchCitationRepository)
	if !ok {
		return SearchHit{}, ErrSearchUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	scope, found, err := s.repo.ResolveSearchScope(ctx, userID, request.KnowledgeBaseID, SearchFilters{})
	if err != nil {
		return SearchHit{}, searchBoundaryError(ctx)
	}
	if err := ctx.Err(); err != nil {
		return SearchHit{}, err
	}
	if !found || scope.Generation != request.Generation {
		return SearchHit{}, ErrNotFound
	}
	if scope.UserID != userID || scope.KnowledgeBaseID != request.KnowledgeBaseID || scope.TenantID != "00000000-0000-4000-8000-000000000001" {
		return SearchHit{}, ErrSearchUnavailable
	}
	chunk, found, err := repo.GetSearchCitation(ctx, scope, request)
	if err != nil {
		return SearchHit{}, searchBoundaryError(ctx)
	}
	if err := ctx.Err(); err != nil {
		return SearchHit{}, err
	}
	if !found || chunk.Candidate.ChunkID != request.ChunkID || chunk.Candidate.DocumentVersionID != request.DocumentVersionID || chunk.Candidate.Generation != request.Generation {
		return SearchHit{}, ErrNotFound
	}
	return searchHit(scope, chunk, 0, []string{}), nil
}
