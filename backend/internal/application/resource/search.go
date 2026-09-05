package resource

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	DefaultSearchKnowledgeBaseID = "00000000-0000-4000-8000-000000000002"
	maxSearchCandidates          = 100
	maxSearchContextBytes        = 16 << 10
)

var (
	ErrSearchInvalid     = errors.New("invalid resource search request")
	ErrSearchUnavailable = errors.New("resource search unavailable")
)

// SearchRequest contains user choices, never a model or an authorization scope.
type SearchRequest struct {
	Query           string
	KnowledgeBaseID string
	TopK            int
	TimeoutMS       int
	Filters         SearchFilters
	TraceID         string
	MaxContextBytes int
}

type SearchFilters struct {
	Type    string `json:"type"`
	Chapter string `json:"chapter"`
	Topic   string `json:"topic"`
}

// SearchScope is resolved from current PostgreSQL state, not HTTP input.
type SearchScope struct {
	UserID                string
	TenantID              string
	KnowledgeBaseID       string
	Generation            int64
	GenerationID          string
	ModelVersionID        string
	Collection            string
	Dimension             int
	Distance              VectorDistance
	Filters               SearchFilters
	ResourceIDs           []string
	ResourceLimitExceeded bool
}

// SearchCandidate intentionally cannot carry source text before final authorization.
type SearchCandidate struct {
	ChunkID           string
	ResourceID        string
	DocumentVersionID string
	Generation        int64
	Score             float64
}

type AuthorizedSearchChunk struct {
	Candidate   SearchCandidate
	Title       string
	Content     string
	QuoteHash   string
	Page        *int
	SectionPath *string
}

// SearchRepository must recheck account, ACL, publication, version and generation
// in AuthorizeSearchChunks, including when the earlier scope still looks valid.
type SearchRepository interface {
	ResolveSearchScope(context.Context, string, string, SearchFilters) (SearchScope, bool, error)
	SearchLexical(context.Context, SearchScope, string, int) ([]SearchCandidate, error)
	AuthorizeSearchChunks(context.Context, SearchScope, []SearchCandidate) ([]AuthorizedSearchChunk, error)
}

// SearchCandidateRetriever is an optional vector recall boundary. Implementations
// must use the administrator's active model and the same coarse authorized scope.
type SearchCandidateRetriever interface {
	RetrieveCandidates(context.Context, SearchScope, string, int) ([]SearchCandidate, error)
}

type SearchCitation struct {
	KnowledgeBaseID   string  `json:"knowledge_base_id"`
	ResourceID        string  `json:"resource_id"`
	DocumentVersionID string  `json:"document_version_id"`
	ChunkID           string  `json:"chunk_id"`
	Generation        int64   `json:"generation"`
	Title             string  `json:"title"`
	Page              *int    `json:"page"`
	SectionPath       *string `json:"section_path"`
	QuoteHash         string  `json:"quote_hash"`
}

type SearchHit struct {
	Content  string         `json:"content"`
	Score    float64        `json:"score"`
	Sources  []string       `json:"sources"`
	Citation SearchCitation `json:"citation"`
}

type SearchResponse struct {
	Items           []SearchHit `json:"items"`
	Adjacent        []SearchHit `json:"adjacent,omitempty"`
	Mode            string      `json:"mode"`
	Degraded        bool        `json:"degraded"`
	DegradedReasons []string    `json:"degraded_reasons"`
	TraceID         string      `json:"trace_id,omitempty"`
}

type SearchService struct {
	repo     SearchRepository
	vector   SearchCandidateRetriever
	reranker SearchReranker
	observer SearchObserver
}

func NewSearchService(repo SearchRepository, vector SearchCandidateRetriever, options ...SearchOption) (*SearchService, error) {
	if repo == nil {
		return nil, errors.New("resource search repository is nil")
	}
	service := &SearchService{repo: repo, vector: vector}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service, nil
}

// Search returns only current, authorized chunks. Provider errors never cross
// this boundary because they may contain queries, document text or credentials.
func (s *SearchService) Search(ctx context.Context, userID string, request SearchRequest) (response SearchResponse, err error) {
	observation := SearchObservation{Stages: make(map[string]time.Duration)}
	started := time.Now()
	defer func() {
		if s.observer != nil {
			observation.Duration = time.Since(started)
			observation.Mode, observation.Failed = response.Mode, err != nil
			observation.References, observation.Empty = len(response.Items)+len(response.Adjacent), len(response.Items) == 0
			observation.DegradedReasons = response.DegradedReasons
			s.observer.ObserveSearch(observation)
		}
	}()
	userID, request, err = normalizeSearchRequest(userID, request)
	if err != nil {
		return SearchResponse{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(request.TimeoutMS)*time.Millisecond)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return SearchResponse{}, err
	}
	response = SearchResponse{
		Items: []SearchHit{}, Mode: "none", DegradedReasons: []string{}, TraceID: request.TraceID,
	}
	scope, found, err := s.repo.ResolveSearchScope(ctx, userID, request.KnowledgeBaseID, request.Filters)
	observation.Stages["scope"] = time.Since(started)
	if err != nil {
		return SearchResponse{}, searchBoundaryError(ctx)
	}
	if err := ctx.Err(); err != nil {
		return SearchResponse{}, err
	}
	if !found {
		return response, nil
	}
	if scope.UserID != userID || scope.KnowledgeBaseID != request.KnowledgeBaseID ||
		scope.TenantID != "00000000-0000-4000-8000-000000000001" || scope.Generation < 1 {
		return SearchResponse{}, ErrSearchUnavailable
	}
	scope.Filters = request.Filters

	// Leave a third of the remaining deadline for the mandatory final SQL check.
	deadline, _ := ctx.Deadline()
	recallCtx, cancelRecall := context.WithTimeout(ctx, time.Until(deadline)*2/3)
	defer cancelRecall()
	type recallResult struct {
		candidates []SearchCandidate
		err        error
		duration   time.Duration
	}
	recallStarted := time.Now()
	lexicalCh := make(chan recallResult, 1)
	go func() {
		start := time.Now()
		items, err := s.repo.SearchLexical(recallCtx, scope, request.Query, maxSearchCandidates)
		lexicalCh <- recallResult{items, err, time.Since(start)}
	}()
	vectorCh := make(chan recallResult, 1)
	if s.vector == nil {
		vectorCh <- recallResult{err: ErrVectorUnavailable}
	} else {
		go func() {
			start := time.Now()
			items, err := s.vector.RetrieveCandidates(recallCtx, scope, request.Query, maxSearchCandidates)
			vectorCh <- recallResult{items, err, time.Since(start)}
		}()
	}
	readRecall := func(ch <-chan recallResult) recallResult {
		select {
		case result := <-ch:
			return result
		case <-recallCtx.Done():
			select {
			case result := <-ch:
				return result
			default:
				return recallResult{err: recallCtx.Err(), duration: time.Since(recallStarted)}
			}
		}
	}
	lexical, vector := readRecall(lexicalCh), readRecall(vectorCh)
	observation.Stages["fts"], observation.Stages["vector"] = lexical.duration, vector.duration
	observation.LexicalCandidates, observation.VectorCandidates = min(len(lexical.candidates), maxSearchCandidates), min(len(vector.candidates), maxSearchCandidates)
	cancelRecall()
	if err := ctx.Err(); err != nil {
		return SearchResponse{}, err
	}
	if lexical.err != nil {
		lexical.candidates = nil
		response.DegradedReasons = append(response.DegradedReasons, "fts_unavailable")
	}
	if vector.err != nil {
		vector.candidates = nil
		response.DegradedReasons = append(response.DegradedReasons, "vector_unavailable")
	}
	response.Degraded = len(response.DegradedReasons) > 0
	if lexical.err != nil && vector.err != nil {
		return response, nil
	}
	response.Mode = "hybrid"
	if vector.err != nil {
		response.Mode = "fts_only"
	} else if lexical.err != nil {
		response.Mode = "vector_only"
	}
	fused := fuseSearchCandidates(lexical.candidates, vector.candidates, scope.Generation)
	if len(fused) == 0 {
		return response, nil
	}
	candidates := make([]SearchCandidate, len(fused))
	for i := range fused {
		candidates[i] = fused[i].candidate
	}
	start := time.Now()
	authorized, err := s.repo.AuthorizeSearchChunks(ctx, scope, candidates)
	observation.Stages["authorize"] = time.Since(start)
	if err != nil {
		return SearchResponse{}, searchBoundaryError(ctx)
	}
	if err := ctx.Err(); err != nil {
		return SearchResponse{}, err
	}
	fused = authorizedSearchOrder(fused, authorized)
	observation.FilteredCandidates = len(candidates) - len(fused)
	fused, err = s.refineSearch(ctx, scope, request, fused, authorized, &response, &observation)
	if err != nil {
		return SearchResponse{}, err
	}
	// Rerank and adjacency may span an ACL or publication change. Never reuse
	// text from the first authorization for a response or a model prompt.
	candidates = make([]SearchCandidate, len(fused))
	for i := range fused {
		candidates[i] = fused[i].candidate
	}
	start = time.Now()
	authorized, err = s.repo.AuthorizeSearchChunks(ctx, scope, candidates)
	observation.Stages["authorize"] += time.Since(start)
	if err != nil {
		return SearchResponse{}, searchBoundaryError(ctx)
	}
	if err := ctx.Err(); err != nil {
		return SearchResponse{}, err
	}
	chunks := searchChunkMap(authorized)
	observation.FilteredCandidates += len(candidates) - len(authorizedSearchOrder(fused, authorized))
	remaining := request.MaxContextBytes
	selectedVersions := make(map[string]bool)
	for _, item := range fused {
		if !item.adjacent && len(response.Items) >= request.TopK {
			continue
		}
		if item.adjacent && !selectedVersions[item.candidate.DocumentVersionID] {
			continue
		}
		chunk, ok := chunks[searchKey(item.candidate)]
		if !ok || strings.TrimSpace(chunk.Content) == "" {
			continue
		}
		used := searchChunkBytes(chunk)
		if used > remaining {
			continue
		}
		remaining -= used
		if item.adjacent {
			response.Adjacent = append(response.Adjacent, searchHit(scope, chunk, item.score, item.sources()))
		} else {
			response.Items = append(response.Items, searchHit(scope, chunk, item.score, item.sources()))
			selectedVersions[item.candidate.DocumentVersionID] = true
		}
	}
	return response, nil
}

func searchBoundaryError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrSearchUnavailable
}

func normalizeSearchRequest(userID string, request SearchRequest) (string, SearchRequest, error) {
	userID = strings.ToLower(strings.TrimSpace(userID))
	request.KnowledgeBaseID = strings.ToLower(strings.TrimSpace(request.KnowledgeBaseID))
	if request.KnowledgeBaseID == "" {
		request.KnowledgeBaseID = DefaultSearchKnowledgeBaseID
	}
	if !isSearchUUID(userID) || !isSearchUUID(request.KnowledgeBaseID) ||
		!validSearchText(request.Query, 2000) || !validSearchText(request.TraceID, 128) ||
		!validSearchText(request.Filters.Chapter, 100) || !validSearchText(request.Filters.Topic, 100) {
		return "", SearchRequest{}, ErrSearchInvalid
	}
	request.Query = strings.Join(strings.Fields(request.Query), " ")
	request.Filters.Type = strings.ToLower(strings.TrimSpace(request.Filters.Type))
	request.Filters.Chapter = strings.TrimSpace(request.Filters.Chapter)
	request.Filters.Topic = strings.TrimSpace(request.Filters.Topic)
	if request.TopK == 0 {
		request.TopK = 5
	}
	if request.TimeoutMS == 0 {
		request.TimeoutMS = 3000
	}
	if request.MaxContextBytes == 0 {
		request.MaxContextBytes = maxSearchContextBytes
	}
	if request.Query == "" || request.TopK < 1 || request.TopK > 20 ||
		request.MaxContextBytes < 1 || request.MaxContextBytes > maxSearchContextBytes ||
		request.TimeoutMS < 100 || request.TimeoutMS > 10000 ||
		(request.Filters.Type != "" && request.Filters.Type != "document" && request.Filters.Type != "video") {
		return "", SearchRequest{}, ErrSearchInvalid
	}
	return userID, request, nil
}

func validSearchText(value string, maxRunes int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= maxRunes &&
		strings.IndexFunc(value, func(r rune) bool { return unicode.IsControl(r) && !unicode.IsSpace(r) }) < 0
}

func isSearchUUID(value string) bool {
	if len(value) != 36 || value == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i, c := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
