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

type EmbeddingRuntimeProvider interface {
	EmbeddingRuntime(context.Context) (adminaiconfigapp.EmbeddingRuntimeConfig, bool, error)
}

type QueryEmbedder struct {
	provider EmbeddingRuntimeProvider
	client   HTTPDoer
}

func NewQueryEmbedder(provider EmbeddingRuntimeProvider, clients ...HTTPDoer) (*QueryEmbedder, error) {
	if provider == nil {
		return nil, errors.New("resource embedding runtime provider is nil")
	}
	client, err := selectHTTPClient(clients)
	if err != nil {
		return nil, err
	}
	return &QueryEmbedder{provider: provider, client: client}, nil
}

func (e *QueryEmbedder) EmbedQuery(ctx context.Context, scope resourceapp.SearchScope, query string) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e == nil || e.provider == nil || !utf8.ValidString(query) || strings.TrimSpace(query) == "" ||
		strings.ContainsRune(query, '\x00') || utf8.RuneCountInString(query) > 2000 {
		return nil, resourceapp.ErrEmbeddingFailed
	}
	runtime, ok, err := e.provider.EmbeddingRuntime(ctx)
	if err != nil || !ok {
		return nil, boundaryError(ctx, resourceapp.ErrEmbeddingFailed)
	}
	version := runtime.Version
	baseURL, err := outbound.NormalizePublicHTTPSBaseURL(runtime.BaseURL)
	if err != nil || strings.TrimSpace(runtime.APIKey) == "" || strings.TrimSpace(runtime.Model) == "" ||
		version.ID != scope.ModelVersionID || version.Status != "active" || version.LogicalName != adminaiconfigapp.ResourceEmbeddingLogicalName ||
		version.Dimension != scope.Dimension || version.Dimension < 1 || version.Dimension > 65536 ||
		version.ProviderModel != runtime.Model || version.Metric != string(scope.Distance) || version.TimeoutSeconds < 1 ||
		version.MaxRetries < 0 || version.MaxTokens < 1 || utf8.RuneCountInString(query) > version.MaxTokens {
		return nil, resourceapp.ErrEmbeddingFailed
	}
	requestCtx, cancel := context.WithTimeout(ctx, min(time.Duration(version.TimeoutSeconds)*time.Second, 10*time.Second))
	defer cancel()
	payload := map[string]any{"model": runtime.Model, "input": []string{query}}
	// Voyage distinguishes query and document embeddings. OpenAI does not expose
	// this field, so retain its existing embeddings contract for other models.
	if strings.HasPrefix(strings.ToLower(runtime.Model), "voyage-") || strings.Contains(strings.ToLower(version.Provider), "voyage") {
		payload["input_type"] = "query"
	}
	if version.SendDimensions {
		payload["dimensions"] = version.Dimension
	}
	for attempt := 0; attempt <= min(version.MaxRetries, 3); attempt++ {
		var response struct {
			Model string `json:"model"`
			Data  []struct {
				Index     *int      `json:"index"`
				Embedding []float32 `json:"embedding"`
			} `json:"data"`
		}
		retryable, err := providerJSON(requestCtx, e.client, baseURL, runtime.APIKey, "/embeddings", payload, &response)
		if err == nil {
			if (response.Model != "" && response.Model != runtime.Model) || len(response.Data) != 1 ||
				response.Data[0].Index == nil || *response.Data[0].Index != 0 || len(response.Data[0].Embedding) != version.Dimension || !validEmbedding(response.Data[0].Embedding) {
				return nil, resourceapp.ErrEmbeddingFailed
			}
			if err := requestCtx.Err(); err != nil {
				return nil, boundaryError(ctx, resourceapp.ErrEmbeddingFailed)
			}
			return response.Data[0].Embedding, nil
		}
		if !retryable || attempt == min(version.MaxRetries, 3) {
			break
		}
		if err := waitForRetry(requestCtx, attempt); err != nil {
			break
		}
	}
	return nil, boundaryError(ctx, resourceapp.ErrEmbeddingFailed)
}

func validEmbedding(vector []float32) bool {
	var nonzero bool
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return false
		}
		nonzero = nonzero || value != 0
	}
	return nonzero
}
