package resourceretrieval

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	adminaiconfigapp "mathstudy/backend/internal/application/adminaiconfig"
	resourceapp "mathstudy/backend/internal/application/resource"
	"mathstudy/backend/internal/platform/outbound"
)

const ResourceRerankerAgentType = "resource_reranker"

type RerankRuntimeProvider interface {
	RuntimeConfigs(context.Context, string) ([]adminaiconfigapp.RuntimeConfig, bool, error)
}

type Reranker struct {
	provider RerankRuntimeProvider
	client   HTTPDoer
}

func NewReranker(provider RerankRuntimeProvider, clients ...HTTPDoer) (*Reranker, error) {
	if provider == nil {
		return nil, errors.New("resource rerank runtime provider is nil")
	}
	client, err := selectHTTPClient(clients)
	if err != nil {
		return nil, err
	}
	return &Reranker{provider: provider, client: client}, nil
}

func (r *Reranker) Rerank(ctx context.Context, query string, chunks []resourceapp.AuthorizedSearchChunk) ([]resourceapp.SearchCandidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r == nil || r.provider == nil {
		return nil, resourceapp.ErrRerankDisabled
	}
	if len(chunks) == 0 {
		return []resourceapp.SearchCandidate{}, nil
	}
	if len(chunks) > 40 || strings.TrimSpace(query) == "" || !utf8.ValidString(query) ||
		strings.ContainsRune(query, '\x00') || utf8.RuneCountInString(query) > 2000 {
		return nil, resourceapp.ErrRerankUnavailable
	}
	documents := make([]string, len(chunks))
	bytesUsed := len(query)
	for i, chunk := range chunks {
		if strings.TrimSpace(chunk.Content) == "" || !utf8.ValidString(chunk.Title) || !utf8.ValidString(chunk.Content) {
			return nil, resourceapp.ErrRerankUnavailable
		}
		documents[i] = chunk.Title + "\n" + chunk.Content
		bytesUsed += len(documents[i])
	}
	if bytesUsed > 64<<10 {
		return nil, resourceapp.ErrRerankUnavailable
	}
	runtimes, configured, err := r.provider.RuntimeConfigs(ctx, ResourceRerankerAgentType)
	if err != nil {
		return nil, boundaryError(ctx, resourceapp.ErrRerankUnavailable)
	}
	if !configured || len(runtimes) == 0 {
		return nil, resourceapp.ErrRerankDisabled
	}
	rankCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for attempt, runtime := range runtimes[:min(len(runtimes), 3)] {
		baseURL, err := outbound.NormalizePublicHTTPSBaseURL(runtime.BaseURL)
		if err != nil || strings.TrimSpace(runtime.APIKey) == "" || strings.TrimSpace(runtime.Model) == "" || runtime.Timeout <= 0 {
			continue
		}
		var response struct {
			Model   string        `json:"model"`
			Data    []rerankEntry `json:"data"`
			Results []rerankEntry `json:"results"`
		}
		requestCtx, cancelRequest := context.WithTimeout(rankCtx, runtime.Timeout)
		retryable, err := providerJSON(requestCtx, r.client, baseURL, runtime.APIKey, "/rerank", map[string]any{
			"model": runtime.Model, "query": query, "documents": documents, "top_k": len(documents), "return_documents": false,
		}, &response)
		cancelRequest()
		if err == nil {
			if response.Model != "" && response.Model != runtime.Model {
				return nil, resourceapp.ErrRerankUnavailable
			}
			entries := response.Data
			if len(entries) == 0 {
				entries = response.Results
			} else if len(response.Results) > 0 {
				return nil, resourceapp.ErrRerankUnavailable
			}
			result, valid := rerankedCandidates(entries, chunks)
			if !valid {
				return nil, resourceapp.ErrRerankUnavailable
			}
			if err := rankCtx.Err(); err != nil {
				return nil, boundaryError(ctx, resourceapp.ErrRerankUnavailable)
			}
			return result, nil
		}
		if !retryable || attempt == min(len(runtimes), 3)-1 {
			break
		}
		if err := waitForRetry(rankCtx, attempt); err != nil {
			break
		}
	}
	return nil, boundaryError(ctx, resourceapp.ErrRerankUnavailable)
}

type rerankEntry struct {
	Index *int     `json:"index"`
	Score *float64 `json:"relevance_score"`
}

func rerankedCandidates(entries []rerankEntry, chunks []resourceapp.AuthorizedSearchChunk) ([]resourceapp.SearchCandidate, bool) {
	if len(entries) != len(chunks) {
		return nil, false
	}
	seen := make(map[int]bool, len(entries))
	result := make([]resourceapp.SearchCandidate, 0, len(entries))
	for _, entry := range entries {
		if entry.Index == nil || entry.Score == nil || *entry.Index < 0 || *entry.Index >= len(chunks) ||
			seen[*entry.Index] || math.IsNaN(*entry.Score) || math.IsInf(*entry.Score, 0) {
			return nil, false
		}
		seen[*entry.Index] = true
		result = append(result, chunks[*entry.Index].Candidate)
	}
	return result, true
}
