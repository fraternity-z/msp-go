package resourceretrieval

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	adminaiconfigapp "mathstudy/backend/internal/application/adminaiconfig"
	resourceapp "mathstudy/backend/internal/application/resource"
	"mathstudy/backend/internal/platform/outbound"
)

const (
	MaxDocumentBatchInputBytes = resourceapp.MaxDocumentEmbeddingBatchBytes
	maxDocumentVectorValues    = 1 << 20
	maxDocumentResponseBytes   = 32 << 20
	documentRequestInterval    = 100 * time.Millisecond
)

type DocumentEmbeddingError struct {
	Code      string
	Retryable bool
}

func (e *DocumentEmbeddingError) Error() string { return "resource document embedding: " + e.Code }

func (e *DocumentEmbeddingError) IngestionFailure() (string, bool) { return e.Code, e.Retryable }

func (e *DocumentEmbeddingError) Is(target error) bool {
	return target == resourceapp.ErrEmbeddingFailed
}

type DocumentEmbedder struct {
	provider    EmbeddingRuntimeProvider
	client      HTTPDoer
	requestSlot chan struct{}
	nextRequest time.Time
}

func NewDocumentEmbedder(provider EmbeddingRuntimeProvider, clients ...HTTPDoer) (*DocumentEmbedder, error) {
	if provider == nil {
		return nil, errors.New("resource document embedding runtime provider is nil")
	}
	client, err := selectHTTPClient(clients)
	if err != nil {
		return nil, err
	}
	if len(clients) == 0 || clients[0] == nil {
		client = outbound.NewPublicHTTPSClient(300 * time.Second)
	}
	return &DocumentEmbedder{provider: provider, client: client, requestSlot: make(chan struct{}, 1)}, nil
}

// CurrentModel returns only the active public contract, never provider credentials.
func (e *DocumentEmbedder) CurrentModel(ctx context.Context) (resourceapp.EmbeddingModel, int, error) {
	runtime, err := e.currentRuntime(ctx)
	if err != nil {
		return resourceapp.EmbeddingModel{}, 0, err
	}
	return documentModel(runtime), documentBatchSize(runtime), nil
}

func (e *DocumentEmbedder) currentRuntime(ctx context.Context) (adminaiconfigapp.EmbeddingRuntimeConfig, error) {
	if err := ctx.Err(); err != nil {
		return adminaiconfigapp.EmbeddingRuntimeConfig{}, err
	}
	if e == nil || e.provider == nil {
		return adminaiconfigapp.EmbeddingRuntimeConfig{}, resourceapp.ErrIngestionModelUnavailable
	}
	runtime, configured, err := e.provider.EmbeddingRuntime(ctx)
	if ctx.Err() != nil {
		return adminaiconfigapp.EmbeddingRuntimeConfig{}, ctx.Err()
	}
	if err != nil || !configured {
		return adminaiconfigapp.EmbeddingRuntimeConfig{}, resourceapp.ErrIngestionModelUnavailable
	}
	version := runtime.Version
	baseURL, err := outbound.NormalizePublicHTTPSBaseURL(runtime.BaseURL)
	if err != nil || strings.TrimSpace(runtime.APIKey) == "" || strings.TrimSpace(runtime.Model) == "" || version.ProviderModel != runtime.Model ||
		version.ID == "" || version.Provider == "" || version.Revision == "" || version.LogicalName != adminaiconfigapp.ResourceEmbeddingLogicalName || version.Status != "active" ||
		version.Dimension < 1 || version.Dimension > 65536 || version.MaxTokens < 4 || version.BatchSize < 1 || version.BatchSize > 256 ||
		version.TimeoutSeconds < 1 || version.TimeoutSeconds > 300 || version.MaxRetries < 0 || version.MaxRetries > 10 ||
		(version.Metric != "cosine" && version.Metric != "dot" && version.Metric != "euclid") ||
		(version.Normalization != nil && *version.Normalization != "" && *version.Normalization != "unicode_nfc") {
		return adminaiconfigapp.EmbeddingRuntimeConfig{}, resourceapp.ErrIngestionModelUnavailable
	}
	runtime.BaseURL = baseURL
	return runtime, nil
}

func documentModel(runtime adminaiconfigapp.EmbeddingRuntimeConfig) resourceapp.EmbeddingModel {
	version := runtime.Version
	return resourceapp.EmbeddingModel{ID: version.ID, Provider: version.Provider, Model: version.ProviderModel, Revision: version.Revision,
		Dimension: version.Dimension, Distance: resourceapp.VectorDistance(version.Metric), MaxTokens: version.MaxTokens}
}

func documentBatchSize(runtime adminaiconfigapp.EmbeddingRuntimeConfig) int {
	return min(runtime.Version.BatchSize, 256, maxDocumentVectorValues/runtime.Version.Dimension)
}

func (e *DocumentEmbedder) Embed(ctx context.Context, request resourceapp.EmbeddingRequest) (resourceapp.EmbeddingResponse, error) {
	if err := ctx.Err(); err != nil {
		return resourceapp.EmbeddingResponse{}, err
	}
	if e == nil || e.client == nil || e.requestSlot == nil {
		return resourceapp.EmbeddingResponse{}, documentEmbeddingError("MODEL_UNAVAILABLE", true)
	}
	select {
	case e.requestSlot <- struct{}{}:
		defer func() { <-e.requestSlot }()
	case <-ctx.Done():
		return resourceapp.EmbeddingResponse{}, ctx.Err()
	}
	runtime, err := e.currentRuntime(ctx)
	if err != nil {
		return resourceapp.EmbeddingResponse{}, documentEmbeddingBoundary(ctx, "MODEL_UNAVAILABLE", true)
	}
	if request.Model != documentModel(runtime) {
		return resourceapp.EmbeddingResponse{}, documentEmbeddingError("MODEL_CONTRACT_MISMATCH", false)
	}
	if err := validateDocumentInputs(request, documentBatchSize(runtime)); err != nil {
		return resourceapp.EmbeddingResponse{}, err
	}
	maxRetries := min(runtime.Version.MaxRetries, 3)
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			if err := waitForRetry(ctx, attempt-1); err != nil {
				return resourceapp.EmbeddingResponse{}, err
			}
			runtime, err = e.currentRuntime(ctx)
			if err != nil {
				return resourceapp.EmbeddingResponse{}, documentEmbeddingBoundary(ctx, "MODEL_UNAVAILABLE", true)
			}
			if request.Model != documentModel(runtime) {
				return resourceapp.EmbeddingResponse{}, documentEmbeddingError("MODEL_CONTRACT_MISMATCH", false)
			}
		}
		if delay := time.Until(e.nextRequest); delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return resourceapp.EmbeddingResponse{}, ctx.Err()
			case <-timer.C:
			}
		}
		e.nextRequest = time.Now().Add(documentRequestInterval)
		requestCtx, cancel := context.WithTimeout(ctx, time.Duration(runtime.Version.TimeoutSeconds)*time.Second)
		payload := map[string]any{"model": runtime.Model, "input": request.Inputs}
		if strings.HasPrefix(strings.ToLower(runtime.Model), "voyage-") || strings.Contains(strings.ToLower(runtime.Version.Provider), "voyage") {
			payload["input_type"], payload["truncation"] = "document", false
		}
		if runtime.Version.SendDimensions {
			payload["dimensions"] = runtime.Version.Dimension
		}
		var response documentEmbeddingResponse
		retryable, err := providerJSONLimited(requestCtx, e.client, runtime.BaseURL, runtime.APIKey, "/embeddings", payload, &response, maxDocumentResponseBytes)
		requestErr := requestCtx.Err()
		cancel()
		if ctx.Err() != nil {
			return resourceapp.EmbeddingResponse{}, ctx.Err()
		}
		if err == nil && requestErr == nil {
			vectors, valid := validateDocumentVectors(response, runtime.Model, len(request.Inputs), runtime.Version.Dimension)
			if !valid {
				return resourceapp.EmbeddingResponse{}, documentEmbeddingError("RESPONSE_INVALID", false)
			}
			return resourceapp.EmbeddingResponse{Model: request.Model, Vectors: vectors}, nil
		}
		if requestErr != nil {
			retryable = true
		}
		if !retryable || attempt == maxRetries {
			return resourceapp.EmbeddingResponse{}, documentEmbeddingError("PROVIDER_UNAVAILABLE", retryable)
		}
	}
	return resourceapp.EmbeddingResponse{}, documentEmbeddingError("PROVIDER_UNAVAILABLE", true)
}

func validateDocumentInputs(request resourceapp.EmbeddingRequest, batchSize int) error {
	if len(request.Inputs) < 1 || len(request.Inputs) > batchSize {
		return documentEmbeddingError("BATCH_LIMIT", false)
	}
	bytesUsed := 0
	for _, input := range request.Inputs {
		if strings.TrimSpace(input) == "" || !utf8.ValidString(input) || strings.ContainsRune(input, '\x00') || !norm.NFC.IsNormalString(input) {
			return documentEmbeddingError("INPUT_INVALID", false)
		}
		if len(input) > request.Model.MaxTokens {
			return documentEmbeddingError("INPUT_TOKEN_BUDGET", false)
		}
		bytesUsed += len(input)
		if bytesUsed > MaxDocumentBatchInputBytes {
			return documentEmbeddingError("BATCH_INPUT_BUDGET", false)
		}
	}
	return nil
}

type documentEmbeddingResponse struct {
	Model string `json:"model"`
	Data  []struct {
		Index     *int      `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

func validateDocumentVectors(response documentEmbeddingResponse, model string, count, dimension int) ([][]float32, bool) {
	if response.Model != "" && response.Model != model || len(response.Data) != count {
		return nil, false
	}
	vectors := make([][]float32, count)
	for _, item := range response.Data {
		if item.Index == nil || *item.Index < 0 || *item.Index >= count || vectors[*item.Index] != nil || len(item.Embedding) != dimension || !validEmbedding(item.Embedding) {
			return nil, false
		}
		vectors[*item.Index] = item.Embedding
	}
	return vectors, true
}

func documentEmbeddingError(code string, retryable bool) error {
	return &DocumentEmbeddingError{Code: code, Retryable: retryable}
}

func documentEmbeddingBoundary(ctx context.Context, code string, retryable bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return documentEmbeddingError(code, retryable)
}
