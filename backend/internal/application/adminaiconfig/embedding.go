package adminaiconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"mathstudy/backend/internal/platform/httpjson"
)

const (
	// ResourceEmbeddingLogicalName is the stable role consumed by resource indexing.
	ResourceEmbeddingLogicalName = "resource_embedding"

	EmbeddingMetricCosine = "cosine"
	EmbeddingMetricDot    = "dot"
	EmbeddingMetricEuclid = "euclid"

	defaultEmbeddingBatchSize = 32
	defaultEmbeddingTimeout   = 30
	maxEmbeddingDimension     = 65536
	maxEmbeddingTokens        = 10_000_000
	maxEmbeddingResponseBytes = 16 << 20
	providerUpdatedAtMetadata = "provider_updated_at"
	modelUpdatedAtMetadata    = "model_updated_at"
)

// EmbeddingModelVersion is an immutable model/vector contract activated by an administrator.
type EmbeddingModelVersion struct {
	ID             string         `json:"id"`
	LogicalName    string         `json:"logical_name"`
	LLMModelID     *string        `json:"llm_model_id"`
	Provider       string         `json:"provider"`
	ProviderModel  string         `json:"provider_model"`
	Revision       string         `json:"revision"`
	Dimension      int            `json:"dimension"`
	Metric         string         `json:"metric"`
	Tokenizer      *string        `json:"tokenizer"`
	Normalization  *string        `json:"normalization"`
	MaxTokens      int            `json:"max_tokens"`
	SendDimensions bool           `json:"send_dimensions"`
	BatchSize      int            `json:"batch_size"`
	TimeoutSeconds int            `json:"timeout_seconds"`
	MaxRetries     int            `json:"max_retries"`
	Status         string         `json:"status"`
	Metadata       map[string]any `json:"metadata"`
	VerifiedAt     *time.Time     `json:"verified_at"`
	ActivatedAt    *time.Time     `json:"activated_at"`
	RetiredAt      *time.Time     `json:"retired_at"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	ProviderName   *string        `json:"provider_name"`
	ModelName      *string        `json:"model_name"`
}

// EmbeddingModelVersionInput is the persistence-ready activation snapshot.
type EmbeddingModelVersionInput struct {
	ID               string
	LogicalName      string
	LLMModelID       string
	Provider         string
	ProviderModel    string
	Revision         string
	Dimension        int
	Metric           string
	Tokenizer        *string
	Normalization    *string
	MaxTokens        int
	SendDimensions   bool
	BatchSize        int
	TimeoutSeconds   int
	MaxRetries       int
	Metadata         map[string]any
	VerifiedAt       time.Time
	ProviderSnapshot EmbeddingProviderSnapshot
}

// EmbeddingProviderSnapshot binds activation to the exact source verified by the probe.
// It is used only during the transaction and is never persisted or serialized.
type EmbeddingProviderSnapshot struct {
	BaseURL             string
	EncryptedCredential string
}

// EmbeddingRuntimeCandidate binds an active immutable snapshot to live encrypted credentials.
type EmbeddingRuntimeCandidate struct {
	Version  EmbeddingModelVersion
	Model    LLMModel
	Provider StoredProvider
}

// EmbeddingRuntimeConfig is returned only inside the process and is never serialized.
type EmbeddingRuntimeConfig struct {
	Version EmbeddingModelVersion
	BaseURL string
	APIKey  string
	Model   string
}

// ConfigureEmbeddingRequest is shared by probe and activation endpoints.
type ConfigureEmbeddingRequest struct {
	ModelID        string `json:"model_id"`
	Revision       string `json:"revision"`
	Dimension      int    `json:"dimension"`
	Metric         string `json:"metric"`
	Tokenizer      string `json:"tokenizer"`
	Normalization  string `json:"normalization"`
	MaxTokens      int    `json:"max_tokens"`
	SendDimensions bool   `json:"send_dimensions"`
	BatchSize      int    `json:"batch_size"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	MaxRetries     int    `json:"max_retries"`
}

// EmbeddingProbeResult contains bounded, non-secret validation evidence.
type EmbeddingProbeResult struct {
	Success           bool    `json:"success"`
	Message           string  `json:"message"`
	LatencyMS         float64 `json:"latency_ms"`
	ModelID           string  `json:"model_id"`
	ProviderModel     string  `json:"provider_model"`
	ObservedDimension int     `json:"observed_dimension"`
}

type normalizedEmbeddingRequest struct {
	ConfigureEmbeddingRequest
	TokenizerValue     *string
	NormalizationValue *string
}

// ListEmbeddingModelVersions returns resource embedding activation history.
func (s *Service) ListEmbeddingModelVersions(ctx context.Context) (ListResponse[EmbeddingModelVersion], error) {
	if err := ctx.Err(); err != nil {
		return ListResponse[EmbeddingModelVersion]{}, err
	}
	items, err := s.repo.ListEmbeddingModelVersions(ctx, ResourceEmbeddingLogicalName)
	if err != nil {
		return ListResponse[EmbeddingModelVersion]{}, err
	}
	return ListResponse[EmbeddingModelVersion]{Items: items, Total: len(items)}, nil
}

// TestEmbeddingModel validates credentials, endpoint behavior, ordering, and vector dimension.
func (s *Service) TestEmbeddingModel(ctx context.Context, request ConfigureEmbeddingRequest) (EmbeddingProbeResult, error) {
	if err := ctx.Err(); err != nil {
		return EmbeddingProbeResult{}, err
	}
	normalized, model, provider, err := s.resolveEmbeddingRequest(ctx, request)
	if err != nil {
		return EmbeddingProbeResult{}, err
	}
	return s.probeEmbeddingModel(ctx, normalized, model, provider)
}

// ActivateEmbeddingModel verifies and atomically activates one immutable resource embedding contract.
func (s *Service) ActivateEmbeddingModel(ctx context.Context, request ConfigureEmbeddingRequest) (EmbeddingModelVersion, error) {
	if err := ctx.Err(); err != nil {
		return EmbeddingModelVersion{}, err
	}
	normalized, model, provider, err := s.resolveEmbeddingRequest(ctx, request)
	if err != nil {
		return EmbeddingModelVersion{}, err
	}
	probe, err := s.probeEmbeddingModel(ctx, normalized, model, provider)
	if err != nil {
		return EmbeddingModelVersion{}, err
	}
	if !probe.Success {
		return EmbeddingModelVersion{}, badRequest("embedding 模型验证失败: " + probe.Message)
	}
	id, err := s.newID()
	if err != nil {
		return EmbeddingModelVersion{}, err
	}
	now := s.now()
	version, err := s.repo.ActivateEmbeddingModelVersion(ctx, EmbeddingModelVersionInput{
		ID:             id,
		LogicalName:    ResourceEmbeddingLogicalName,
		LLMModelID:     model.ID,
		Provider:       provider.Code,
		ProviderModel:  model.ModelID,
		Revision:       normalized.Revision,
		Dimension:      normalized.Dimension,
		Metric:         normalized.Metric,
		Tokenizer:      normalized.TokenizerValue,
		Normalization:  normalized.NormalizationValue,
		MaxTokens:      normalized.MaxTokens,
		SendDimensions: normalized.SendDimensions,
		BatchSize:      normalized.BatchSize,
		TimeoutSeconds: normalized.TimeoutSeconds,
		MaxRetries:     normalized.MaxRetries,
		Metadata:       embeddingSourceMetadata(model, provider),
		VerifiedAt:     now,
		ProviderSnapshot: EmbeddingProviderSnapshot{
			BaseURL:             provider.BaseURL,
			EncryptedCredential: provider.EncryptedAPIKey,
		},
	}, now)
	if err != nil {
		return EmbeddingModelVersion{}, normalizeRepositoryError(err)
	}
	return version, nil
}

func embeddingSourceMetadata(model LLMModel, provider StoredProvider) map[string]any {
	return map[string]any{
		providerUpdatedAtMetadata: provider.UpdatedAt.UTC().Format(time.RFC3339Nano),
		modelUpdatedAtMetadata:    model.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

// EmbeddingSourceVersionMatches verifies that an active snapshot still references its verified source revision.
func EmbeddingSourceVersionMatches(version EmbeddingModelVersion, model LLMModel, provider StoredProvider) bool {
	providerUpdatedAt, providerOK := version.Metadata[providerUpdatedAtMetadata].(string)
	modelUpdatedAt, modelOK := version.Metadata[modelUpdatedAtMetadata].(string)
	return providerOK && modelOK &&
		providerUpdatedAt == provider.UpdatedAt.UTC().Format(time.RFC3339Nano) &&
		modelUpdatedAt == model.UpdatedAt.UTC().Format(time.RFC3339Nano)
}

// EmbeddingRuntime resolves the active administrator-managed resource embedding configuration.
func (s *Service) EmbeddingRuntime(ctx context.Context) (EmbeddingRuntimeConfig, bool, error) {
	if err := ctx.Err(); err != nil {
		return EmbeddingRuntimeConfig{}, false, err
	}
	candidate, ok, err := s.repo.GetActiveEmbeddingRuntimeCandidate(ctx, ResourceEmbeddingLogicalName)
	if err != nil || !ok {
		return EmbeddingRuntimeConfig{}, ok, err
	}
	baseURL, err := normalizeBaseURL(candidate.Provider.BaseURL)
	if err != nil {
		return EmbeddingRuntimeConfig{}, false, nil
	}
	apiKey, err := s.nextProviderAPIKey(candidate.Provider.ID, candidate.Provider.EncryptedAPIKey)
	if err != nil {
		return EmbeddingRuntimeConfig{}, false, nil
	}
	return EmbeddingRuntimeConfig{
		Version: candidate.Version,
		BaseURL: openAIAPIBaseURL(baseURL),
		APIKey:  apiKey,
		Model:   candidate.Model.ModelID,
	}, true, nil
}

func (s *Service) resolveEmbeddingRequest(ctx context.Context, request ConfigureEmbeddingRequest) (normalizedEmbeddingRequest, LLMModel, StoredProvider, error) {
	normalized, err := normalizeEmbeddingRequest(request)
	if err != nil {
		return normalizedEmbeddingRequest{}, LLMModel{}, StoredProvider{}, err
	}
	model, ok, err := s.repo.GetModel(ctx, normalized.ModelID)
	if err != nil {
		return normalizedEmbeddingRequest{}, LLMModel{}, StoredProvider{}, err
	}
	if !ok {
		return normalizedEmbeddingRequest{}, LLMModel{}, StoredProvider{}, badRequest("model_id 不存在")
	}
	provider, ok, err := s.repo.GetProvider(ctx, model.ProviderID)
	if err != nil {
		return normalizedEmbeddingRequest{}, LLMModel{}, StoredProvider{}, err
	}
	if !ok {
		return normalizedEmbeddingRequest{}, LLMModel{}, StoredProvider{}, badRequest("模型关联渠道不存在")
	}
	if !model.IsActive || !provider.IsActive {
		return normalizedEmbeddingRequest{}, LLMModel{}, StoredProvider{}, badRequest("请选择已启用的渠道和模型")
	}
	return normalized, model, provider, nil
}

func normalizeEmbeddingRequest(request ConfigureEmbeddingRequest) (normalizedEmbeddingRequest, error) {
	request.ModelID = strings.TrimSpace(request.ModelID)
	request.Revision = strings.TrimSpace(request.Revision)
	request.Metric = strings.ToLower(strings.TrimSpace(request.Metric))
	if request.BatchSize == 0 {
		request.BatchSize = defaultEmbeddingBatchSize
	}
	if request.TimeoutSeconds == 0 {
		request.TimeoutSeconds = defaultEmbeddingTimeout
	}
	if request.ModelID == "" {
		return normalizedEmbeddingRequest{}, badRequest("model_id 不能为空")
	}
	if request.Revision == "" || len([]rune(request.Revision)) > 100 {
		return normalizedEmbeddingRequest{}, badRequest("revision 长度必须在 1 到 100 之间")
	}
	if request.Dimension < 1 || request.Dimension > maxEmbeddingDimension {
		return normalizedEmbeddingRequest{}, badRequest("dimension 必须在 1 到 65536 之间")
	}
	switch request.Metric {
	case EmbeddingMetricCosine, EmbeddingMetricDot, EmbeddingMetricEuclid:
	default:
		return normalizedEmbeddingRequest{}, badRequest("metric 仅支持 cosine、dot 或 euclid")
	}
	if request.MaxTokens < 1 || request.MaxTokens > maxEmbeddingTokens {
		return normalizedEmbeddingRequest{}, badRequest("max_tokens 必须在 1 到 10000000 之间")
	}
	if request.BatchSize < 1 || request.BatchSize > 256 {
		return normalizedEmbeddingRequest{}, badRequest("batch_size 必须在 1 到 256 之间")
	}
	if request.TimeoutSeconds < 1 || request.TimeoutSeconds > 300 {
		return normalizedEmbeddingRequest{}, badRequest("timeout_seconds 必须在 1 到 300 之间")
	}
	if request.MaxRetries < 0 || request.MaxRetries > 10 {
		return normalizedEmbeddingRequest{}, badRequest("max_retries 必须在 0 到 10 之间")
	}
	tokenizer, err := optionalEmbeddingString(request.Tokenizer, 100, "tokenizer")
	if err != nil {
		return normalizedEmbeddingRequest{}, err
	}
	normalization, err := optionalEmbeddingString(request.Normalization, 50, "normalization")
	if err != nil {
		return normalizedEmbeddingRequest{}, err
	}
	return normalizedEmbeddingRequest{
		ConfigureEmbeddingRequest: request,
		TokenizerValue:            tokenizer,
		NormalizationValue:        normalization,
	}, nil
}

func optionalEmbeddingString(value string, maxRunes int, field string) (*string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if len([]rune(value)) > maxRunes {
		return nil, badRequest(fmt.Sprintf("%s 长度不能超过 %d", field, maxRunes))
	}
	return &value, nil
}

func (s *Service) probeEmbeddingModel(ctx context.Context, request normalizedEmbeddingRequest, model LLMModel, provider StoredProvider) (EmbeddingProbeResult, error) {
	result := EmbeddingProbeResult{ModelID: model.ID, ProviderModel: model.ModelID}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	apiKey, err := s.nextProviderAPIKey(provider.ID, provider.EncryptedAPIKey)
	if err != nil {
		result.Message = "API 密钥不可用"
		return result, nil
	}
	baseURL, err := normalizeBaseURL(provider.BaseURL)
	if err != nil {
		return EmbeddingProbeResult{}, err
	}
	payload := map[string]any{
		"model":           model.ModelID,
		"input":           []string{"MathStudyPlatform embedding configuration probe"},
		"encoding_format": "float",
	}
	if request.SendDimensions {
		payload["dimensions"] = request.Dimension
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return EmbeddingProbeResult{}, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, time.Duration(request.TimeoutSeconds)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodPost, joinProviderURL(baseURL, "/v1/embeddings"), bytes.NewReader(body))
	if err != nil {
		return EmbeddingProbeResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	startedAt := time.Now()
	client := s.httpClient
	if httpClient, ok := client.(*http.Client); ok {
		clone := *httpClient
		clone.Timeout = time.Duration(request.TimeoutSeconds) * time.Second
		client = &clone
	}
	resp, err := client.Do(req)
	result.LatencyMS = float64(time.Since(startedAt).Microseconds()) / 1000
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return result, ctxErr
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
			result.Message = "请求超时"
		} else {
			result.Message = "无法连接上游 embeddings 接口"
		}
		return result, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.Message = fmt.Sprintf("上游返回 HTTP %d", resp.StatusCode)
		return result, nil
	}
	var response struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := httpjson.DecodeLimited(resp.Body, maxEmbeddingResponseBytes, &response); err != nil {
		result.Message = "embeddings 响应格式无效"
		return result, nil
	}
	if len(response.Data) != 1 || response.Data[0].Index != 0 || len(response.Data[0].Embedding) == 0 {
		result.Message = "embeddings 响应顺序或向量为空"
		return result, nil
	}
	result.ObservedDimension = len(response.Data[0].Embedding)
	if result.ObservedDimension != request.Dimension {
		result.Message = fmt.Sprintf("返回维度为 %d，与配置的 %d 不一致", result.ObservedDimension, request.Dimension)
		return result, nil
	}
	result.Success = true
	result.Message = "embedding 模型验证通过"
	return result, nil
}
