package resourcehttp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	authapp "mathstudy/backend/internal/application/auth"
	resourceapp "mathstudy/backend/internal/application/resource"
	"mathstudy/backend/internal/platform/httpauth"
	"mathstudy/backend/internal/platform/httpjson"
	"mathstudy/backend/internal/platform/httpquery"
	"mathstudy/backend/internal/platform/httpvalidate"
	"mathstudy/backend/internal/platform/redact"
)

// Service is the resource application surface used by HTTP handlers.
type Service interface {
	GetResources(context.Context, string, resourceapp.ListFilter) (resourceapp.ListResponse, error)
	GetFavorites(context.Context, string, int, int) (resourceapp.ListResponse, error)
	GetResource(context.Context, string, string) (resourceapp.Resource, error)
	CreateResource(context.Context, string, resourceapp.ResourceInput) (resourceapp.Resource, error)
	UpdateResource(context.Context, string, string, resourceapp.ResourceUpdate) (resourceapp.Resource, error)
	DeleteResource(context.Context, string, string) error
	ToggleFavorite(context.Context, string, string) (resourceapp.FavoriteToggleResponse, error)
	GetStats(context.Context, string) (resourceapp.Stats, error)
}

// Authenticator validates access tokens against current account state.
type Authenticator interface {
	DecodeActiveAccessToken(context.Context, string) (authapp.Principal, bool, error)
}

// Handler serves /resources endpoints.
type Handler struct {
	service          Service
	searchService    SearchService
	ingestionService IngestionService
	auth             Authenticator
	logger           *slog.Logger
}

// NewHandler creates a resource HTTP handler.
func NewHandler(logger *slog.Logger, service Service, auth Authenticator, options ...Option) (*Handler, error) {
	if service == nil {
		return nil, errors.New("resource service is nil")
	}
	if auth == nil {
		return nil, errors.New("resource authenticator is nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	handler := &Handler{service: service, auth: auth, logger: logger}
	for _, option := range options {
		if option != nil {
			option(handler)
		}
	}
	return handler, nil
}

// Register attaches resource routes under prefix, for example /api/v1/resources.
func (h *Handler) Register(mux *http.ServeMux, prefix string) {
	mux.HandleFunc("GET "+prefix, h.list)
	mux.HandleFunc("GET "+prefix+"/stats", h.stats)
	mux.HandleFunc("GET "+prefix+"/favorites", h.favorites)
	mux.HandleFunc("GET "+prefix+"/{resource_id}", h.detail)
	mux.HandleFunc("POST "+prefix, h.create)
	mux.HandleFunc("POST "+prefix+"/search", h.search)
	h.registerIngestion(mux, prefix+"/ingestions")
	mux.HandleFunc("GET "+prefix+"/citations/{chunk_id}", h.citation)
	mux.HandleFunc("PUT "+prefix+"/{resource_id}", h.update)
	mux.HandleFunc("DELETE "+prefix+"/{resource_id}", h.delete)
	mux.HandleFunc("POST "+prefix+"/{resource_id}/favorite", h.toggleFavorite)
}

type createRequest struct {
	Title       string   `json:"title"`
	Type        string   `json:"type"`
	Body        string   `json:"body"`
	Chapter     *string  `json:"chapter"`
	Topic       *string  `json:"topic"`
	Tags        []string `json:"tags"`
	Difficulty  *float64 `json:"difficulty"`
	StorageType string   `json:"storage_type"`
	URL         *string  `json:"url"`
	Duration    *string  `json:"duration"`
	Pages       *int     `json:"pages"`
	Source      *string  `json:"source"`
}

type updateRequest struct {
	Title       *string   `json:"title"`
	Type        *string   `json:"type"`
	Body        *string   `json:"body"`
	Chapter     *string   `json:"chapter"`
	Topic       *string   `json:"topic"`
	Tags        *[]string `json:"tags"`
	Difficulty  *float64  `json:"difficulty"`
	StorageType *string   `json:"storage_type"`
	URL         *string   `json:"url"`
	Duration    *string   `json:"duration"`
	Pages       *int      `json:"pages"`
	Source      *string   `json:"source"`
}

const maxJSONBodyBytes = 2 << 20

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	filter, ok := parseListFilter(w, r)
	if !ok {
		return
	}
	response, err := h.service.GetResources(r.Context(), principal.UserID, filter)
	if err != nil {
		h.logger.Error("get resource list failed", "error", redact.String(err.Error()))
		writeResourceError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "获取资源列表失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) stats(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	response, err := h.service.GetStats(r.Context(), principal.UserID)
	if err != nil {
		h.logger.Error("get resource stats failed", "error", redact.String(err.Error()))
		writeResourceError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "获取资源统计失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) favorites(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	page, pageSize, ok := parsePage(w, r)
	if !ok {
		return
	}
	response, err := h.service.GetFavorites(r.Context(), principal.UserID, page, pageSize)
	if err != nil {
		h.logger.Error("get favorite resources failed", "error", redact.String(err.Error()))
		writeResourceError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "获取收藏列表失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) detail(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	response, err := h.service.GetResource(r.Context(), principal.UserID, r.PathValue("resource_id"))
	if err != nil {
		if errors.Is(err, resourceapp.ErrNotFound) {
			writeResourceError(w, http.StatusNotFound, "NOT_FOUND", "资源不存在")
			return
		}
		h.logger.Error("get resource detail failed", "error", redact.String(err.Error()))
		writeResourceError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "获取资源详情失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requireTeacher(w, r)
	if !ok {
		return
	}
	var request createRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	input, ok := request.toInput(w)
	if !ok {
		return
	}
	response, err := h.service.CreateResource(r.Context(), principal.UserID, input)
	if err != nil {
		if errors.Is(err, resourceapp.ErrBadRequest) {
			writeResourceError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", redact.String(err.Error()))
			return
		}
		h.logger.Error("create resource failed", "error", redact.String(err.Error()))
		writeResourceError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "创建资源失败")
		return
	}
	httpjson.Write(w, http.StatusCreated, response)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requireTeacher(w, r)
	if !ok {
		return
	}
	var request updateRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	input, ok := request.toUpdate(w)
	if !ok {
		return
	}
	response, err := h.service.UpdateResource(r.Context(), r.PathValue("resource_id"), principal.UserID, input)
	if err != nil {
		if errors.Is(err, resourceapp.ErrIngestionConflict) {
			writeResourceError(w, http.StatusConflict, "INGESTION_CONFLICT", "已入库文档的原文件和正文不可直接修改")
			return
		}
		if errors.Is(err, resourceapp.ErrBadRequest) {
			writeResourceError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", redact.String(err.Error()))
			return
		}
		if errors.Is(err, resourceapp.ErrNotFound) {
			writeResourceError(w, http.StatusNotFound, "NOT_FOUND", "资源不存在或无权限修改")
			return
		}
		h.logger.Error("update resource failed", "error", redact.String(err.Error()))
		writeResourceError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "更新资源失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requireTeacher(w, r)
	if !ok {
		return
	}
	err := h.service.DeleteResource(r.Context(), r.PathValue("resource_id"), principal.UserID)
	if err != nil {
		if errors.Is(err, resourceapp.ErrNotFound) {
			writeResourceError(w, http.StatusNotFound, "NOT_FOUND", "资源不存在或无权限删除")
			return
		}
		h.logger.Error("delete resource failed", "error", redact.String(err.Error()))
		writeResourceError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "删除资源失败")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) toggleFavorite(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	response, err := h.service.ToggleFavorite(r.Context(), principal.UserID, r.PathValue("resource_id"))
	if err != nil {
		if errors.Is(err, resourceapp.ErrNotFound) {
			writeResourceError(w, http.StatusNotFound, "NOT_FOUND", "资源不存在")
			return
		}
		h.logger.Error("toggle resource favorite failed", "error", redact.String(err.Error()))
		writeResourceError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "切换收藏状态失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) requirePrincipal(w http.ResponseWriter, r *http.Request) (authapp.Principal, bool) {
	return httpauth.RequireBearerAccessContext(w, r, h.auth.DecodeActiveAccessToken, nil, "", writeResourceError)
}

func (h *Handler) requireTeacher(w http.ResponseWriter, r *http.Request) (authapp.Principal, bool) {
	return httpauth.RequireBearerAccessContext(
		w, r, h.auth.DecodeActiveAccessToken, authapp.IsTeacherOrAdmin,
		"权限不足，需要教师权限", writeResourceError,
	)
}

func parseListFilter(w http.ResponseWriter, r *http.Request) (resourceapp.ListFilter, bool) {
	page, pageSize, ok := parsePage(w, r)
	if !ok {
		return resourceapp.ListFilter{}, false
	}
	query := r.URL.Query()
	resourceType := query.Get("type")
	if resourceType != "" && !validResourceType(resourceType) {
		writeResourceError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "type 必须是 video 或 document")
		return resourceapp.ListFilter{}, false
	}
	return resourceapp.ListFilter{
		Type:          resourceType,
		Chapter:       query.Get("chapter"),
		Topic:         query.Get("topic"),
		Search:        query.Get("search"),
		FavoritesOnly: parseBool(query.Get("favorites_only")),
		Page:          page,
		PageSize:      pageSize,
	}, true
}

func parsePage(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	pagination, err := httpquery.Pagination(r.URL.Query(), 20, 100)
	if err != nil {
		writeResourcePaginationError(w, err)
		return 0, 0, false
	}
	return pagination.Page, pagination.PageSize, true
}

func writeResourcePaginationError(w http.ResponseWriter, err error) {
	writeResourceError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", httpquery.PaginationErrorMessage(err, 100))
}

func parseBool(value string) bool {
	parsed, err := strconv.ParseBool(value)
	return err == nil && parsed
}

func (r createRequest) toInput(w http.ResponseWriter) (resourceapp.ResourceInput, bool) {
	if !validateTitle(w, &r.Title) || !validateResourceType(w, r.Type) || !validateStorageType(w, r.StorageType) {
		return resourceapp.ResourceInput{}, false
	}
	if !httpvalidate.OptionalString(w, r.Chapter, 100, "chapter", writeResourceError) ||
		!httpvalidate.OptionalString(w, r.Topic, 100, "topic", writeResourceError) ||
		!httpvalidate.OptionalString(w, r.Source, 200, "source", writeResourceError) ||
		!validateOptionalPages(w, r.Pages) {
		return resourceapp.ResourceInput{}, false
	}
	difficulty := 0.5
	if r.Difficulty != nil {
		if *r.Difficulty < 0 || *r.Difficulty > 1 {
			writeResourceError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "difficulty 必须在 0 到 1 之间")
			return resourceapp.ResourceInput{}, false
		}
		difficulty = *r.Difficulty
	}
	storageType := r.StorageType
	if strings.TrimSpace(storageType) == "" {
		storageType = "external"
	}
	if r.Tags == nil {
		r.Tags = []string{}
	}
	return resourceapp.ResourceInput{
		Title:       r.Title,
		Type:        strings.ToLower(strings.TrimSpace(r.Type)),
		Body:        r.Body,
		Chapter:     r.Chapter,
		Topic:       r.Topic,
		Tags:        r.Tags,
		Difficulty:  difficulty,
		StorageType: strings.ToLower(strings.TrimSpace(storageType)),
		URL:         r.URL,
		Duration:    r.Duration,
		Pages:       r.Pages,
		Source:      r.Source,
	}, true
}

func (r updateRequest) toUpdate(w http.ResponseWriter) (resourceapp.ResourceUpdate, bool) {
	if !validateTitle(w, r.Title) {
		return resourceapp.ResourceUpdate{}, false
	}
	if r.Type != nil && !validateResourceType(w, *r.Type) {
		return resourceapp.ResourceUpdate{}, false
	}
	if r.StorageType != nil && !validateStorageType(w, *r.StorageType) {
		return resourceapp.ResourceUpdate{}, false
	}
	if r.Difficulty != nil && (*r.Difficulty < 0 || *r.Difficulty > 1) {
		writeResourceError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "difficulty 必须在 0 到 1 之间")
		return resourceapp.ResourceUpdate{}, false
	}
	if !httpvalidate.OptionalString(w, r.Chapter, 100, "chapter", writeResourceError) ||
		!httpvalidate.OptionalString(w, r.Topic, 100, "topic", writeResourceError) ||
		!httpvalidate.OptionalString(w, r.Source, 200, "source", writeResourceError) ||
		!validateOptionalPages(w, r.Pages) {
		return resourceapp.ResourceUpdate{}, false
	}
	var tags []string
	tagsSet := false
	if r.Tags != nil {
		tags = *r.Tags
		if tags == nil {
			tags = []string{}
		}
		tagsSet = true
	}
	var resourceType *string
	if r.Type != nil {
		value := strings.ToLower(strings.TrimSpace(*r.Type))
		resourceType = &value
	}
	var storageType *string
	if r.StorageType != nil {
		value := strings.ToLower(strings.TrimSpace(*r.StorageType))
		storageType = &value
	}
	return resourceapp.ResourceUpdate{
		Title:       r.Title,
		Type:        resourceType,
		Body:        r.Body,
		Chapter:     r.Chapter,
		Topic:       r.Topic,
		Tags:        tags,
		TagsSet:     tagsSet,
		Difficulty:  r.Difficulty,
		StorageType: storageType,
		URL:         r.URL,
		Duration:    r.Duration,
		Pages:       r.Pages,
		Source:      r.Source,
	}, true
}

func validateTitle(w http.ResponseWriter, value *string) bool {
	if value == nil {
		return true
	}
	if len(*value) < 1 || len(*value) > 500 {
		writeResourceError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "title 长度必须在 1 到 500 之间")
		return false
	}
	return true
}

func validateResourceType(w http.ResponseWriter, value string) bool {
	if validResourceType(value) {
		return true
	}
	writeResourceError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "type 必须是 video 或 document")
	return false
}

func validResourceType(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "video", "document":
		return true
	default:
		return false
	}
}

func validateStorageType(w http.ResponseWriter, value string) bool {
	if strings.TrimSpace(value) == "" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "local", "cloud", "external":
		return true
	default:
		writeResourceError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "storage_type 必须是 local、cloud 或 external")
		return false
	}
}

func validateOptionalPages(w http.ResponseWriter, value *int) bool {
	if value == nil || *value >= 1 {
		return true
	}
	writeResourceError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "pages 必须大于等于 1")
	return false
}

func decodeRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	return httpjson.DecodeStrictOrBadRequest(w, r, maxJSONBodyBytes, target)
}

func writeResourceError(w http.ResponseWriter, status int, code, message string) {
	httpjson.WriteDetailError(w, status, code, message)
}
