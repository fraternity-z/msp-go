package adminaiconfighthttp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	adminaiconfigapp "mathstudy/backend/internal/application/adminaiconfig"
	authapp "mathstudy/backend/internal/application/auth"
	"mathstudy/backend/internal/platform/httpauth"
	"mathstudy/backend/internal/platform/httpjson"
	"mathstudy/backend/internal/platform/redact"
)

// Service is the admin AI config application surface used by HTTP handlers.
type Service interface {
	ListProviders(context.Context, bool) (adminaiconfigapp.ListResponse[adminaiconfigapp.LLMProvider], error)
	GetProvider(context.Context, string) (adminaiconfigapp.LLMProvider, error)
	CreateProvider(context.Context, adminaiconfigapp.CreateProviderRequest) (adminaiconfigapp.LLMProvider, error)
	CreateProviderWithModels(context.Context, adminaiconfigapp.CreateProviderWithModelsRequest) (adminaiconfigapp.ProviderWithModelsResponse, error)
	UpdateProvider(context.Context, string, adminaiconfigapp.UpdateProviderRequest) (adminaiconfigapp.LLMProvider, error)
	DeleteProvider(context.Context, string) (adminaiconfigapp.SuccessResponse, error)
	TestProvider(context.Context, string, string) (adminaiconfigapp.ProviderTestResult, error)
	FetchAvailableModels(context.Context, string) (adminaiconfigapp.FetchModelsResponse, error)
	FetchModelsByCredentials(context.Context, adminaiconfigapp.FetchModelsByCredentialsRequest) (adminaiconfigapp.FetchModelsResponse, error)
	UpdateProviderModels(context.Context, string, adminaiconfigapp.ModelsUpdateRequest) (adminaiconfigapp.ModelsUpdateResult, error)
	ListModels(context.Context, string, bool) (adminaiconfigapp.ListResponse[adminaiconfigapp.LLMModel], error)
	GetModel(context.Context, string) (adminaiconfigapp.LLMModel, error)
	CreateModel(context.Context, adminaiconfigapp.CreateModelRequest) (adminaiconfigapp.LLMModel, error)
	UpdateModel(context.Context, string, adminaiconfigapp.UpdateModelRequest) (adminaiconfigapp.LLMModel, error)
	DeleteModel(context.Context, string) (adminaiconfigapp.SuccessResponse, error)
	SetDefaultModel(context.Context, string) (adminaiconfigapp.SuccessResponse, error)
	ListEmbeddingModelVersions(context.Context) (adminaiconfigapp.ListResponse[adminaiconfigapp.EmbeddingModelVersion], error)
	TestEmbeddingModel(context.Context, adminaiconfigapp.ConfigureEmbeddingRequest) (adminaiconfigapp.EmbeddingProbeResult, error)
	ActivateEmbeddingModel(context.Context, adminaiconfigapp.ConfigureEmbeddingRequest) (adminaiconfigapp.EmbeddingModelVersion, error)
	ListAgentConfigs(context.Context) (adminaiconfigapp.ListResponse[adminaiconfigapp.AgentModelConfig], error)
	ListAgentTypes(context.Context) (adminaiconfigapp.AgentTypesResponse, error)
	GetAgentConfig(context.Context, string) (adminaiconfigapp.AgentModelConfig, error)
	UpdateAgentConfig(context.Context, string, adminaiconfigapp.UpdateAgentConfigRequest) (adminaiconfigapp.AgentModelConfig, error)
	DeleteAgentConfig(context.Context, string) (adminaiconfigapp.SuccessResponse, error)
}

// Authenticator validates access tokens against current account state.
type Authenticator interface {
	DecodeActiveAccessToken(context.Context, string) (authapp.Principal, bool, error)
}

// Handler serves /admin/ai-config provider, model, and agent settings.
type Handler struct {
	service Service
	auth    Authenticator
	logger  *slog.Logger
}

// NewHandler creates an admin AI config handler.
func NewHandler(logger *slog.Logger, service Service, auth Authenticator) (*Handler, error) {
	if service == nil {
		return nil, errors.New("admin ai config service is nil")
	}
	if auth == nil {
		return nil, errors.New("admin ai config authenticator is nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{service: service, auth: auth, logger: logger}, nil
}

// Register attaches admin AI config routes under prefix, for example /api/v1/admin/ai-config.
func (h *Handler) Register(mux *http.ServeMux, prefix string) {
	mux.HandleFunc("GET "+prefix+"/providers", h.listProviders)
	mux.HandleFunc("POST "+prefix+"/providers", h.createProvider)
	mux.HandleFunc("POST "+prefix+"/providers/with-models", h.createProviderWithModels)
	mux.HandleFunc("POST "+prefix+"/channels/fetch-models", h.fetchModelsByCredentials)
	mux.HandleFunc("GET "+prefix+"/providers/{provider_id}", h.getProvider)
	mux.HandleFunc("PUT "+prefix+"/providers/{provider_id}", h.updateProvider)
	mux.HandleFunc("DELETE "+prefix+"/providers/{provider_id}", h.deleteProvider)
	mux.HandleFunc("POST "+prefix+"/providers/{provider_id}/test", h.testProvider)
	mux.HandleFunc("GET "+prefix+"/providers/{provider_id}/fetch-models", h.fetchProviderModels)
	mux.HandleFunc("PUT "+prefix+"/providers/{provider_id}/models", h.updateProviderModels)
	mux.HandleFunc("GET "+prefix+"/models", h.listModels)
	mux.HandleFunc("POST "+prefix+"/models", h.createModel)
	mux.HandleFunc("GET "+prefix+"/models/{model_id}", h.getModel)
	mux.HandleFunc("PUT "+prefix+"/models/{model_id}", h.updateModel)
	mux.HandleFunc("DELETE "+prefix+"/models/{model_id}", h.deleteModel)
	mux.HandleFunc("POST "+prefix+"/models/{model_id}/set-default", h.setDefaultModel)
	mux.HandleFunc("GET "+prefix+"/embeddings", h.listEmbeddingModels)
	mux.HandleFunc("POST "+prefix+"/embeddings/test", h.testEmbeddingModel)
	mux.HandleFunc("PUT "+prefix+"/embeddings/active", h.activateEmbeddingModel)
	mux.HandleFunc("GET "+prefix+"/agents", h.listAgentConfigs)
	mux.HandleFunc("GET "+prefix+"/agents/types", h.listAgentTypes)
	mux.HandleFunc("GET "+prefix+"/agents/{agent_type}", h.getAgentConfig)
	mux.HandleFunc("PUT "+prefix+"/agents/{agent_type}", h.updateAgentConfig)
	mux.HandleFunc("DELETE "+prefix+"/agents/{agent_type}", h.deleteAgentConfig)
}

func (h *Handler) listProviders(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	response, err := h.service.ListProviders(r.Context(), parseBoolQuery(r, "include_inactive"))
	if err != nil {
		h.writeServiceError(w, err, "获取 AI 渠道列表失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) getProvider(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	response, err := h.service.GetProvider(r.Context(), r.PathValue("provider_id"))
	if err != nil {
		h.writeServiceError(w, err, "获取 AI 渠道失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) createProvider(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	var request adminaiconfigapp.CreateProviderRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	response, err := h.service.CreateProvider(r.Context(), request)
	if err != nil {
		h.writeServiceError(w, err, "创建 AI 渠道失败")
		return
	}
	httpjson.Write(w, http.StatusCreated, response)
}

func (h *Handler) createProviderWithModels(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	var request adminaiconfigapp.CreateProviderWithModelsRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	response, err := h.service.CreateProviderWithModels(r.Context(), request)
	if err != nil {
		h.writeServiceError(w, err, "创建 AI 渠道失败")
		return
	}
	httpjson.Write(w, http.StatusCreated, response)
}

func (h *Handler) updateProvider(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	var request adminaiconfigapp.UpdateProviderRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	response, err := h.service.UpdateProvider(r.Context(), r.PathValue("provider_id"), request)
	if err != nil {
		h.writeServiceError(w, err, "更新 AI 渠道失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) deleteProvider(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	response, err := h.service.DeleteProvider(r.Context(), r.PathValue("provider_id"))
	if err != nil {
		h.writeServiceError(w, err, "删除 AI 渠道失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) testProvider(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	response, err := h.service.TestProvider(r.Context(), r.PathValue("provider_id"), r.URL.Query().Get("model_id"))
	if err != nil {
		h.writeServiceError(w, err, "测试 AI 渠道失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) fetchProviderModels(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	response, err := h.service.FetchAvailableModels(r.Context(), r.PathValue("provider_id"))
	if err != nil {
		h.writeServiceError(w, err, "获取模型列表失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) fetchModelsByCredentials(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	var request adminaiconfigapp.FetchModelsByCredentialsRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	response, err := h.service.FetchModelsByCredentials(r.Context(), request)
	if err != nil {
		h.writeServiceError(w, err, "获取模型列表失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) updateProviderModels(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	var request adminaiconfigapp.ModelsUpdateRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	response, err := h.service.UpdateProviderModels(r.Context(), r.PathValue("provider_id"), request)
	if err != nil {
		h.writeServiceError(w, err, "更新模型列表失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) listModels(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	response, err := h.service.ListModels(r.Context(), r.URL.Query().Get("provider_id"), parseBoolQuery(r, "include_inactive"))
	if err != nil {
		h.writeServiceError(w, err, "获取模型列表失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) getModel(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	response, err := h.service.GetModel(r.Context(), r.PathValue("model_id"))
	if err != nil {
		h.writeServiceError(w, err, "获取模型失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) createModel(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	var request adminaiconfigapp.CreateModelRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	response, err := h.service.CreateModel(r.Context(), request)
	if err != nil {
		h.writeServiceError(w, err, "创建模型失败")
		return
	}
	httpjson.Write(w, http.StatusCreated, response)
}

func (h *Handler) updateModel(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	var request adminaiconfigapp.UpdateModelRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	response, err := h.service.UpdateModel(r.Context(), r.PathValue("model_id"), request)
	if err != nil {
		h.writeServiceError(w, err, "更新模型失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) deleteModel(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	response, err := h.service.DeleteModel(r.Context(), r.PathValue("model_id"))
	if err != nil {
		h.writeServiceError(w, err, "删除模型失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) setDefaultModel(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	response, err := h.service.SetDefaultModel(r.Context(), r.PathValue("model_id"))
	if err != nil {
		h.writeServiceError(w, err, "设置默认模型失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) listEmbeddingModels(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	response, err := h.service.ListEmbeddingModelVersions(r.Context())
	if err != nil {
		h.writeServiceError(w, err, "获取向量模型配置失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) testEmbeddingModel(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	var request adminaiconfigapp.ConfigureEmbeddingRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	response, err := h.service.TestEmbeddingModel(r.Context(), request)
	if err != nil {
		h.writeServiceError(w, err, "测试向量模型失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) activateEmbeddingModel(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	var request adminaiconfigapp.ConfigureEmbeddingRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	response, err := h.service.ActivateEmbeddingModel(r.Context(), request)
	if err != nil {
		h.writeServiceError(w, err, "激活向量模型失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) listAgentConfigs(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	response, err := h.service.ListAgentConfigs(r.Context())
	if err != nil {
		h.writeServiceError(w, err, "获取智能体配置失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) listAgentTypes(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	response, err := h.service.ListAgentTypes(r.Context())
	if err != nil {
		h.writeServiceError(w, err, "获取智能体类型失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) getAgentConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	response, err := h.service.GetAgentConfig(r.Context(), r.PathValue("agent_type"))
	if err != nil {
		h.writeServiceError(w, err, "获取智能体配置失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) updateAgentConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	var request adminaiconfigapp.UpdateAgentConfigRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	response, err := h.service.UpdateAgentConfig(r.Context(), r.PathValue("agent_type"), request)
	if err != nil {
		h.writeServiceError(w, err, "更新智能体配置失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) deleteAgentConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	response, err := h.service.DeleteAgentConfig(r.Context(), r.PathValue("agent_type"))
	if err != nil {
		h.writeServiceError(w, err, "重置智能体配置失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) requireAdmin(w http.ResponseWriter, r *http.Request) (authapp.Principal, bool) {
	return httpauth.RequireBearerAccessContext(
		w, r, h.auth.DecodeActiveAccessToken, authapp.IsAdmin,
		"权限不足，需要管理员权限", writeAIConfigError,
	)
}

func (h *Handler) writeServiceError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, adminaiconfigapp.ErrBadRequest):
		writeAIConfigError(w, http.StatusBadRequest, "BAD_REQUEST", redact.String(err.Error()))
	case errors.Is(err, adminaiconfigapp.ErrNotFound):
		writeAIConfigError(w, http.StatusNotFound, "NOT_FOUND", "AI 配置不存在")
	case errors.Is(err, adminaiconfigapp.ErrConflict):
		writeAIConfigError(w, http.StatusConflict, "CONFLICT", redact.String(err.Error()))
	default:
		h.logger.Error("admin ai config request failed", "error", redact.String(err.Error()))
		writeAIConfigError(w, http.StatusInternalServerError, "INTERNAL_ERROR", fallback)
	}
}

func decodeRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	return httpjson.DecodeStrictOrDetailError(w, r, 1<<20, target)
}

func parseBoolQuery(r *http.Request, name string) bool {
	value := strings.TrimSpace(r.URL.Query().Get(name))
	return strings.EqualFold(value, "true") || value == "1"
}

func writeAIConfigError(w http.ResponseWriter, status int, code string, message string) {
	httpjson.WriteDetailError(w, status, code, message)
}
