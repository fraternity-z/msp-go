package resourcehttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	resourceapp "mathstudy/backend/internal/application/resource"
	"mathstudy/backend/internal/platform/httpjson"
)

type IngestionService interface {
	Upload(context.Context, string, resourceapp.DocumentUpload) (resourceapp.IngestionStatus, error)
	Get(context.Context, string, string) (resourceapp.IngestionStatus, error)
	List(context.Context, string, int, int) (resourceapp.IngestionList, error)
	Retry(context.Context, string, string) (resourceapp.IngestionStatus, error)
	Withdraw(context.Context, string, string, bool) (resourceapp.IngestionStatus, error)
}

func WithIngestionService(service IngestionService) Option {
	return func(h *Handler) { h.ingestionService = service }
}

func (h *Handler) registerIngestion(mux *http.ServeMux, prefix string) {
	mux.HandleFunc("POST "+prefix, h.uploadDocument)
	mux.HandleFunc("GET "+prefix, h.listIngestions)
	mux.HandleFunc("GET "+prefix+"/{resource_id}", h.ingestionStatus)
	mux.HandleFunc("POST "+prefix+"/{resource_id}/retry", h.retryIngestion)
	mux.HandleFunc("POST "+prefix+"/{resource_id}/unpublish", h.unpublishIngestion)
	mux.HandleFunc("DELETE "+prefix+"/{resource_id}", h.deleteIngestion)
}

func (h *Handler) ingestionOwner(w http.ResponseWriter, r *http.Request) (string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	principal, ok := h.requireTeacher(w, r)
	if !ok {
		return "", false
	}
	if h.ingestionService == nil {
		h.writeIngestionError(w, resourceapp.ErrIngestionUnavailable)
		return "", false
	}
	return principal.UserID, true
}

func (h *Handler) uploadDocument(w http.ResponseWriter, r *http.Request) {
	owner, ok := h.ingestionOwner(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, resourceapp.MaxDocumentBytes+(1<<20))
	// #nosec G120 -- MaxBytesReader bounds the full multipart request.
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeResourceError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "文件不能超过 50 MiB")
		} else {
			h.writeIngestionError(w, resourceapp.ErrIngestionInvalid)
		}
		return
	}
	defer r.MultipartForm.RemoveAll()
	allowed := map[string]bool{"title": true, "chapter": true, "topic": true, "client_request_id": true}
	for key, values := range r.MultipartForm.Value {
		if !allowed[key] || len(values) != 1 {
			h.writeIngestionError(w, resourceapp.ErrIngestionInvalid)
			return
		}
	}
	if len(r.MultipartForm.File) != 1 || len(r.MultipartForm.File["file"]) != 1 {
		h.writeIngestionError(w, resourceapp.ErrIngestionInvalid)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		h.writeIngestionError(w, resourceapp.ErrIngestionInvalid)
		return
	}
	defer file.Close()
	if header.Size > resourceapp.MaxDocumentBytes {
		writeResourceError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "文件不能超过 50 MiB")
		return
	}
	result, err := h.ingestionService.Upload(r.Context(), owner, resourceapp.DocumentUpload{
		Title: r.FormValue("title"), Chapter: r.FormValue("chapter"), Topic: r.FormValue("topic"), ClientRequestID: r.FormValue("client_request_id"),
		Filename: header.Filename, MIMEType: header.Header.Get("Content-Type"), ByteSize: header.Size, Reader: file,
	})
	if err != nil {
		h.writeIngestionError(w, err)
		return
	}
	httpjson.Write(w, http.StatusAccepted, result)
}

func (h *Handler) listIngestions(w http.ResponseWriter, r *http.Request) {
	owner, ok := h.ingestionOwner(w, r)
	if !ok {
		return
	}
	page, size, ok := parsePage(w, r)
	if !ok {
		return
	}
	result, err := h.ingestionService.List(r.Context(), owner, page, size)
	if err != nil {
		h.writeIngestionError(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, result)
}

func (h *Handler) ingestionStatus(w http.ResponseWriter, r *http.Request) {
	owner, ok := h.ingestionOwner(w, r)
	if !ok {
		return
	}
	result, err := h.ingestionService.Get(r.Context(), owner, r.PathValue("resource_id"))
	if err != nil {
		h.writeIngestionError(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, result)
}

func (h *Handler) retryIngestion(w http.ResponseWriter, r *http.Request) {
	h.changeIngestion(w, r, "retry")
}

func (h *Handler) unpublishIngestion(w http.ResponseWriter, r *http.Request) {
	h.changeIngestion(w, r, "unpublish")
}

func (h *Handler) deleteIngestion(w http.ResponseWriter, r *http.Request) {
	h.changeIngestion(w, r, "delete")
}

func (h *Handler) changeIngestion(w http.ResponseWriter, r *http.Request, action string) {
	owner, ok := h.ingestionOwner(w, r)
	if !ok {
		return
	}
	if r.ContentLength != 0 {
		var empty map[string]json.RawMessage
		if !httpjson.DecodeStrictOrBadRequest(w, r, 1024, &empty) {
			return
		}
		if empty == nil || len(empty) != 0 {
			h.writeIngestionError(w, resourceapp.ErrIngestionInvalid)
			return
		}
	}
	var result resourceapp.IngestionStatus
	var err error
	if action == "retry" {
		result, err = h.ingestionService.Retry(r.Context(), owner, r.PathValue("resource_id"))
	} else {
		result, err = h.ingestionService.Withdraw(r.Context(), owner, r.PathValue("resource_id"), action == "delete")
	}
	if err != nil {
		h.writeIngestionError(w, err)
		return
	}
	status := http.StatusOK
	if action == "retry" {
		status = http.StatusAccepted
	}
	httpjson.Write(w, status, result)
}

func (h *Handler) writeIngestionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.Canceled):
		return
	case errors.Is(err, context.DeadlineExceeded):
		writeResourceError(w, http.StatusGatewayTimeout, "INGESTION_TIMEOUT", "文档登记超时，请重试")
	case errors.Is(err, resourceapp.ErrIngestionInvalid):
		writeResourceError(w, http.StatusBadRequest, "INVALID_INGESTION", "文档或登记参数无效")
	case errors.Is(err, resourceapp.ErrObjectUnsupported):
		writeResourceError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_DOCUMENT", "仅支持有效的 PDF、DOCX、TXT 和 Markdown 文档")
	case errors.Is(err, resourceapp.ErrNotFound):
		writeResourceError(w, http.StatusNotFound, "INGESTION_NOT_FOUND", "文档不存在或无权限访问")
	case errors.Is(err, resourceapp.ErrIngestionConflict):
		writeResourceError(w, http.StatusConflict, "INGESTION_CONFLICT", "文档状态已变化或重复请求的内容不一致")
	case errors.Is(err, resourceapp.ErrIngestionQueueFull):
		w.Header().Set("Retry-After", "30")
		writeResourceError(w, http.StatusTooManyRequests, "INGESTION_QUEUE_FULL", "处理队列繁忙，请稍后重试")
	case errors.Is(err, resourceapp.ErrIngestionModelUnavailable):
		writeResourceError(w, http.StatusServiceUnavailable, "EMBEDDING_UNAVAILABLE", "文档模型暂不可用")
	case errors.Is(err, resourceapp.ErrIngestionUnavailable):
		writeResourceError(w, http.StatusServiceUnavailable, "INGESTION_UNAVAILABLE", "文档处理暂不可用")
	default:
		h.logger.Error("resource ingestion request failed", "error_code", "ingestion_internal")
		writeResourceError(w, http.StatusInternalServerError, "INGESTION_ERROR", "文档处理失败，请稍后重试")
	}
}
