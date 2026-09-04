package adminaiconfig

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	defaultEmbeddingMaxTokens = 8192
	maxEmbeddingDimension     = 65536
	maxEmbeddingTokens        = 10_000_000
	maxEmbeddingResponseBytes = 16 << 20
	maxEmbeddingProbeRetries  = 20
	embeddingProbeRetryDelay  = 250 * time.Millisecond
	embeddingProbeMaxDelay    = 2 * time.Second
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
	ResolvedRevision  string  `json:"resolved_revision"`
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
	normalized.Dimension = probe.ObservedDimension
	normalized.Revision = probe.ResolvedRevision
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
	if len([]rune(request.Revision)) > 100 {
		return normalizedEmbeddingRequest{}, badRequest("revision 长度不能超过 100")
	}
	if request.Dimension < 0 || request.Dimension > maxEmbeddingDimension {
		return normalizedEmbeddingRequest{}, badRequest("dimension 必须在 0 到 65536 之间")
	}
	if request.SendDimensions && request.Dimension == 0 {
		return normalizedEmbeddingRequest{}, badRequest("send_dimensions 为 true 时 dimension 必须显式大于 0")
	}
	if request.Metric == "" {
		request.Metric = EmbeddingMetricCosine
	}
	switch request.Metric {
	case EmbeddingMetricCosine, EmbeddingMetricDot, EmbeddingMetricEuclid:
	default:
		return normalizedEmbeddingRequest{}, badRequest("metric 仅支持 cosine、dot 或 euclid")
	}
	if request.MaxTokens == 0 {
		request.MaxTokens = defaultEmbeddingMaxTokens
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
	if strings.TrimSpace(request.Normalization) == "" {
		request.Normalization = "unicode_nfc"
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

type embeddingProbeAttemptResult struct {
	dimension int
	message   string
	retryable bool
	attempts  int
}

func (s *Service) probeEmbeddingModel(ctx context.Context, request normalizedEmbeddingRequest, model LLMModel, provider StoredProvider) (EmbeddingProbeResult, error) {
	result := EmbeddingProbeResult{ModelID: model.ID, ProviderModel: model.ModelID}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	keyring, err := s.decryptProviderKeyring(provider.EncryptedAPIKey)
	if err != nil || len(keyring.Keys) == 0 {
		result.Message = "API 密钥不可用"
		return result, nil
	}
	baseURL, err := normalizeBaseURL(provider.BaseURL)
	if err != nil {
		return EmbeddingProbeResult{}, err
	}
	payload := map[string]any{
		"model": model.ModelID,
		"input": []string{"MathStudyPlatform embedding configuration probe"},
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
	startedAt := time.Now()
	client := s.httpClient
	if httpClient, ok := client.(*http.Client); ok {
		clone := *httpClient
		clone.Timeout = time.Duration(request.TimeoutSeconds) * time.Second
		client = &clone
	}
	endpoint := joinProviderURL(baseURL, "/v1/embeddings")
	observedDimension := 0
	remainingRetries := maxEmbeddingProbeRetries
	for keyIndex, apiKey := range keyring.Keys {
		keyRetries := min(request.MaxRetries, remainingRetries)
		attempt, err := probeEmbeddingAPIKey(probeCtx, client, endpoint, body, apiKey, keyRetries)
		result.LatencyMS = float64(time.Since(startedAt).Microseconds()) / 1000
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return result, ctxErr
			}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
				result.Message = "请求超时"
				return result, nil
			}
			return result, err
		}
		remainingRetries -= max(attempt.attempts-1, 0)
		if attempt.message != "" {
			if len(keyring.Keys) > 1 {
				result.Message = fmt.Sprintf("第 %d/%d 个 API 密钥验证失败：%s", keyIndex+1, len(keyring.Keys), attempt.message)
			} else {
				result.Message = attempt.message
			}
			return result, nil
		}
		if request.Dimension > 0 && attempt.dimension != request.Dimension {
			result.Message = fmt.Sprintf("返回维度为 %d，与配置的 %d 不一致", attempt.dimension, request.Dimension)
			return result, nil
		}
		if observedDimension == 0 {
			observedDimension = attempt.dimension
			continue
		}
		if attempt.dimension != observedDimension {
			result.Message = "不同 API 密钥返回的向量维度不一致"
			return result, nil
		}
	}
	result.ObservedDimension = observedDimension
	result.ResolvedRevision = resolvedEmbeddingRevision(request, model, provider, observedDimension)
	result.Success = true
	result.Message = "embedding 模型验证通过"
	if len(keyring.Keys) > 1 {
		result.Message = fmt.Sprintf("embedding 模型验证通过（已检查 %d 个 API 密钥）", len(keyring.Keys))
	}
	return result, nil
}

func probeEmbeddingAPIKey(ctx context.Context, client HTTPDoer, endpoint string, body []byte, apiKey string, maxRetries int) (embeddingProbeAttemptResult, error) {
	var result embeddingProbeAttemptResult
	for attempt := 0; attempt <= maxRetries; attempt++ {
		current, err := performEmbeddingProbe(ctx, client, endpoint, body, apiKey)
		if err != nil {
			return embeddingProbeAttemptResult{}, err
		}
		current.attempts = attempt + 1
		result = current
		if current.message == "" || !current.retryable || attempt == maxRetries {
			return result, nil
		}
		if err := waitForEmbeddingProbeRetry(ctx, attempt); err != nil {
			return embeddingProbeAttemptResult{}, err
		}
	}
	return result, nil
}

func performEmbeddingProbe(ctx context.Context, client HTTPDoer, endpoint string, body []byte, apiKey string) (embeddingProbeAttemptResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return embeddingProbeAttemptResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return embeddingProbeAttemptResult{}, ctxErr
		}
		return embeddingProbeAttemptResult{message: "无法连接上游 embeddings 接口", retryable: true}, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return embeddingProbeAttemptResult{
			message:   fmt.Sprintf("上游返回 HTTP %d", resp.StatusCode),
			retryable: resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError,
		}, nil
	}
	var response struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := httpjson.DecodeLimited(resp.Body, maxEmbeddingResponseBytes, &response); err != nil {
		return embeddingProbeAttemptResult{message: "embeddings 响应格式无效"}, nil
	}
	if len(response.Data) != 1 || response.Data[0].Index != 0 || len(response.Data[0].Embedding) == 0 {
		return embeddingProbeAttemptResult{message: "embeddings 响应顺序或向量为空"}, nil
	}
	dimension := len(response.Data[0].Embedding)
	if dimension > maxEmbeddingDimension {
		return embeddingProbeAttemptResult{message: fmt.Sprintf("返回维度 %d 超出支持范围", dimension)}, nil
	}
	return embeddingProbeAttemptResult{dimension: dimension}, nil
}

func waitForEmbeddingProbeRetry(ctx context.Context, attempt int) error {
	delay := embeddingProbeRetryDelay * time.Duration(1<<min(attempt, 3))
	if delay > embeddingProbeMaxDelay {
		delay = embeddingProbeMaxDelay
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func resolvedEmbeddingRevision(request normalizedEmbeddingRequest, model LLMModel, provider StoredProvider, dimension int) string {
	if request.Revision != "" {
		return request.Revision
	}
	tokenizer := ""
	if request.TokenizerValue != nil {
		tokenizer = *request.TokenizerValue
	}
	normalization := ""
	if request.NormalizationValue != nil {
		normalization = *request.NormalizationValue
	}
	contract := struct {
		ProviderID        string `json:"provider_id"`
		Provider          string `json:"provider"`
		ProviderBaseURL   string `json:"provider_base_url"`
		ProviderUpdatedAt string `json:"provider_updated_at"`
		LLMModelID        string `json:"llm_model_id"`
		Model             string `json:"model"`
		ModelUpdatedAt    string `json:"model_updated_at"`
		Dimension         int    `json:"dimension"`
		Metric            string `json:"metric"`
		Tokenizer         string `json:"tokenizer"`
		Normalization     string `json:"normalization"`
		MaxTokens         int    `json:"max_tokens"`
		SendDimensions    bool   `json:"send_dimensions"`
		BatchSize         int    `json:"batch_size"`
		TimeoutSeconds    int    `json:"timeout_seconds"`
		MaxRetries        int    `json:"max_retries"`
	}{
		ProviderID:        provider.ID,
		Provider:          provider.Code,
		ProviderBaseURL:   strings.TrimRight(strings.TrimSpace(provider.BaseURL), "/"),
		ProviderUpdatedAt: provider.UpdatedAt.UTC().Format(time.RFC3339Nano),
		LLMModelID:        model.ID,
		Model:             model.ModelID,
		ModelUpdatedAt:    model.UpdatedAt.UTC().Format(time.RFC3339Nano),
		Dimension:         dimension,
		Metric:            request.Metric,
		Tokenizer:         tokenizer,
		Normalization:     normalization,
		MaxTokens:         request.MaxTokens,
		SendDimensions:    request.SendDimensions,
		BatchSize:         request.BatchSize,
		TimeoutSeconds:    request.TimeoutSeconds,
		MaxRetries:        request.MaxRetries,
	}
	payload, _ := json.Marshal(contract)
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("auto-v2-%x", sum[:8])
}
