package resourcehttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	resourceapp "mathstudy/backend/internal/application/resource"
	"mathstudy/backend/internal/platform/httpjson"
)

const maxSearchBodyBytes = 16 << 10

// SearchService retrieves only currently authorized resource chunks.
type SearchService interface {
	Search(context.Context, string, resourceapp.SearchRequest) (resourceapp.SearchResponse, error)
}

type CitationService interface {
	GetCitation(context.Context, string, resourceapp.CitationRequest) (resourceapp.SearchHit, error)
}

func (h *Handler) citation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	service, ok := h.searchService.(CitationService)
	if !ok {
		h.writeSearchError(w, resourceapp.ErrSearchUnavailable, w.Header().Get("X-Request-ID"))
		return
	}
	query := r.URL.Query()
	generation, err := strconv.ParseInt(query.Get("generation"), 10, 64)
	if err != nil {
		h.writeSearchError(w, resourceapp.ErrSearchInvalid, w.Header().Get("X-Request-ID"))
		return
	}
	response, err := service.GetCitation(r.Context(), principal.UserID, resourceapp.CitationRequest{
		KnowledgeBaseID: query.Get("knowledge_base_id"), ChunkID: r.PathValue("chunk_id"),
		DocumentVersionID: query.Get("document_version_id"), Generation: generation,
	})
	if errors.Is(err, resourceapp.ErrNotFound) {
		writeResourceError(w, http.StatusNotFound, "CITATION_UNAVAILABLE", "引用已失效或无权查看")
		return
	}
	if err != nil {
		h.writeSearchError(w, err, w.Header().Get("X-Request-ID"))
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

// Option customizes the resource HTTP handler.
type Option func(*Handler)

// WithSearchService enables resource retrieval alongside existing resource routes.
func WithSearchService(service SearchService) Option {
	return func(handler *Handler) {
		handler.searchService = service
	}
}

type searchRequest struct {
	Query           string        `json:"query"`
	KnowledgeBaseID string        `json:"knowledge_base_id"`
	TopK            int           `json:"top_k"`
	TimeoutMS       int           `json:"timeout_ms"`
	Filters         searchFilters `json:"filters"`
}

type searchFilters struct {
	Type    string `json:"type"`
	Chapter string `json:"chapter"`
	Topic   string `json:"topic"`
}

func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.searchService == nil {
		writeResourceError(w, http.StatusServiceUnavailable, "SEARCH_UNAVAILABLE", "资源检索暂不可用，请稍后重试")
		return
	}
	request, ok := decodeSearchRequest(w, r)
	if !ok {
		return
	}
	// RequestID middleware validates the value before placing it on the response.
	traceID := w.Header().Get("X-Request-ID")
	response, err := h.searchService.Search(r.Context(), principal.UserID, resourceapp.SearchRequest{
		Query:           request.Query,
		KnowledgeBaseID: request.KnowledgeBaseID,
		TopK:            request.TopK,
		TimeoutMS:       request.TimeoutMS,
		Filters: resourceapp.SearchFilters{
			Type:    request.Filters.Type,
			Chapter: request.Filters.Chapter,
			Topic:   request.Filters.Topic,
		},
		TraceID: traceID,
	})
	if err != nil {
		h.writeSearchError(w, err, traceID)
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func decodeSearchRequest(w http.ResponseWriter, r *http.Request) (searchRequest, bool) {
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSearchBodyBytes))
	decoder.DisallowUnknownFields()
	var request *searchRequest
	err := decoder.Decode(&request)
	if err == nil && request != nil {
		if trailingErr := decoder.Decode(&struct{}{}); trailingErr == io.EOF {
			return *request, true
		} else {
			err = trailingErr
		}
	}
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		writeResourceError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "请求体超过大小限制")
	} else {
		writeResourceError(w, http.StatusBadRequest, "BAD_REQUEST", "资源检索请求格式无效")
	}
	return searchRequest{}, false
}

func (h *Handler) writeSearchError(w http.ResponseWriter, err error, traceID string) {
	switch {
	case errors.Is(err, context.Canceled):
		return
	case errors.Is(err, context.DeadlineExceeded):
		writeResourceError(w, http.StatusGatewayTimeout, "REQUEST_TIMEOUT", "资源检索超时，请稍后重试")
	case errors.Is(err, resourceapp.ErrSearchInvalid):
		writeResourceError(w, http.StatusBadRequest, "SEARCH_INVALID", "资源检索参数无效")
	case errors.Is(err, resourceapp.ErrSearchUnavailable):
		h.logger.Warn("resource search unavailable", "request_id", traceID, "code", "SEARCH_UNAVAILABLE")
		writeResourceError(w, http.StatusServiceUnavailable, "SEARCH_UNAVAILABLE", "资源检索暂不可用，请稍后重试")
	default:
		h.logger.Error("resource search failed", "request_id", traceID, "code", "INTERNAL_ERROR")
		writeResourceError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "资源检索失败，请稍后重试")
	}
}
