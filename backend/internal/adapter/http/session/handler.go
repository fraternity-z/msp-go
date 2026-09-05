package sessionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	airiskapp "mathstudy/backend/internal/application/airisk"
	authapp "mathstudy/backend/internal/application/auth"
	sessionapp "mathstudy/backend/internal/application/session"
	"mathstudy/backend/internal/platform/httpauth"
	"mathstudy/backend/internal/platform/httpjson"
	"mathstudy/backend/internal/platform/httpquery"
	"mathstudy/backend/internal/platform/redact"
)

// Service is the session application surface used by HTTP handlers.
type Service interface {
	CreateSession(context.Context, string, *string, string) (sessionapp.CreateSessionResponse, error)
	StartChat(context.Context, string, string, *string, string, string, []string, sessionapp.StartChatNotifier, sessionapp.ChatStreamCallbacks) (sessionapp.ChatResult, error)
	ProcessChat(context.Context, string, string, string, []string, sessionapp.ChatStreamCallbacks) (sessionapp.ChatResult, error)
	GetHistory(context.Context, string, string, int, int) (sessionapp.HistoryResponse, error)
	GetSessions(context.Context, string, int, int, bool) (sessionapp.SessionListResponse, error)
	EndSession(context.Context, string, string) (sessionapp.EndResponse, error)
	UpdateSessionMode(context.Context, string, string, string) (sessionapp.UpdateModeResponse, error)
	DeleteSession(context.Context, string, string) (sessionapp.DeleteResponse, error)
	BatchDeleteSessions(context.Context, []string, string) (sessionapp.BatchDeleteResponse, error)
	CancelTask(context.Context, string, string) (sessionapp.CancelTaskResponse, error)
}

// Authenticator validates access tokens against current account state.
type Authenticator interface {
	DecodeActiveAccessToken(context.Context, string) (authapp.Principal, bool, error)
}

// Handler serves /session endpoints.
type Handler struct {
	service Service
	auth    Authenticator
	logger  *slog.Logger
}

// NewHandler creates a session HTTP handler.
func NewHandler(logger *slog.Logger, service Service, auth Authenticator) (*Handler, error) {
	if service == nil {
		return nil, errors.New("session service is nil")
	}
	if auth == nil {
		return nil, errors.New("session authenticator is nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{service: service, auth: auth, logger: logger}, nil
}

// Register attaches session routes under prefix, for example /api/v1/session.
func (h *Handler) Register(mux *http.ServeMux, prefix string) {
	mux.HandleFunc("POST "+prefix+"/start", h.start)
	mux.HandleFunc("POST "+prefix+"/start-chat", h.startChat)
	mux.HandleFunc("GET "+prefix+"/list", h.list)
	mux.HandleFunc("POST "+prefix+"/batch-delete", h.batchDelete)
	mux.HandleFunc("POST "+prefix+"/task/{task_id}/cancel", h.cancelTask)
	mux.HandleFunc("POST "+prefix+"/{session_id}/chat", h.chat)
	mux.HandleFunc("GET "+prefix+"/{session_id}/history", h.history)
	mux.HandleFunc("POST "+prefix+"/{session_id}/end", h.end)
	mux.HandleFunc("PATCH "+prefix+"/{session_id}/mode", h.updateMode)
	mux.HandleFunc("DELETE "+prefix+"/{session_id}", h.delete)
}

type startRequest struct {
	Topic *string `json:"topic"`
	Mode  string  `json:"mode"`
}

type chatRequest struct {
	Message     string   `json:"message"`
	Attachments []string `json:"attachments"`
}

type startChatRequest struct {
	SessionID   string   `json:"session_id"`
	Topic       *string  `json:"topic"`
	Mode        string   `json:"mode"`
	Message     string   `json:"message"`
	Attachments []string `json:"attachments"`
}

type updateModeRequest struct {
	Mode string `json:"mode"`
}

type batchDeleteRequest struct {
	SessionIDs []string `json:"session_ids"`
}

type chatSSEWriter struct {
	response     http.ResponseWriter
	started      bool
	taskWritten  bool
	chunkWritten bool
	err          error
}

func (h *Handler) start(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	var request startRequest
	if r.Body != nil && r.ContentLength != 0 {
		if !decodeRequest(w, r, &request) {
			return
		}
	}
	if request.Mode == "" {
		request.Mode = "chat"
	}
	response, err := h.service.CreateSession(r.Context(), principal.UserID, request.Topic, request.Mode)
	if err != nil {
		if errors.Is(err, sessionapp.ErrInvalidMode) {
			writeSessionError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "不支持的会话模式")
			return
		}
		h.logSessionError("create session failed", err)
		writeSessionError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "创建会话失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) chat(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	var request chatRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.Message) == "" {
		writeSessionError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "消息内容不能为空")
		return
	}
	stream := &chatSSEWriter{response: w}
	result, err := h.service.ProcessChat(
		r.Context(),
		r.PathValue("session_id"),
		principal.UserID,
		request.Message,
		request.Attachments,
		stream.callbacks(),
	)
	if err != nil {
		h.writeChatStreamFailure(stream, err, "process chat failed")
		return
	}
	if err := stream.writeResult(result); err != nil {
		h.logger.Debug("session chat stream closed before completion", "error", redact.String(err.Error()))
	}
}

func (h *Handler) startChat(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	var request startChatRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	if request.Mode == "" {
		request.Mode = "chat"
	}

	stream := &chatSSEWriter{response: w}
	result, err := h.service.StartChat(
		r.Context(),
		principal.UserID,
		request.SessionID,
		request.Topic,
		request.Mode,
		request.Message,
		request.Attachments,
		func(session sessionapp.StartChatSession) {
			if err := stream.writeEvent("session_info", session); err != nil {
				h.logger.Debug("first chat stream closed before generation", "error", redact.String(err.Error()))
			}
		},
		stream.callbacks(),
	)
	if err != nil {
		h.writeChatStreamFailure(stream, err, "start chat failed")
		return
	}
	if err := stream.writeResult(result); err != nil {
		h.logger.Debug("first chat stream closed before completion", "error", redact.String(err.Error()))
	}
}

func (h *Handler) writeChatStreamFailure(stream *chatSSEWriter, err error, logMessage string) {
	if !stream.started {
		h.writeChatFailure(stream.response, err, logMessage)
		return
	}
	persistenceFailure := sessionapp.IsChatPersistenceError(err)
	if persistenceFailure {
		h.logSessionError(logMessage, err)
	}
	if stream.err != nil || (!persistenceFailure && errors.Is(err, context.Canceled)) {
		h.logger.Debug("session chat stream canceled")
		return
	}

	code, message := "PROCESSING_ERROR", "处理消息时发生错误，请稍后重试"
	switch {
	case persistenceFailure:
		// A repository failure must remain visible even when it wraps a
		// cancellation or deadline from the interrupted write context.
		code, message = "PROCESSING_ERROR", "回复保存失败，请稍后重试"
	case errors.Is(err, context.DeadlineExceeded):
		code, message = "REQUEST_TIMEOUT", "请求处理超时，请稍后重试"
	case errors.Is(err, sessionapp.ErrStartChatInProgress):
		code, message = "FIRST_CHAT_IN_PROGRESS", "首次消息仍在处理中，请稍后同步会话历史"
	case errors.Is(err, sessionapp.ErrFirstChatCannotResume):
		code, message = "FIRST_CHAT_NOT_RESUMABLE", "会话历史已发生变化，无法安全补写首次回复"
	default:
		if riskCode, riskMessage, ok := aiRiskSSEError(err); ok {
			code, message = riskCode, riskMessage
		} else {
			h.logSessionError(logMessage, err)
		}
	}
	if writeErr := stream.writeEvent("error", map[string]string{"type": "error", "code": code, "message": message}); writeErr != nil {
		h.logger.Debug("session chat stream closed while writing error", "error", redact.String(writeErr.Error()))
	}
}

func (h *Handler) writeChatFailure(w http.ResponseWriter, err error, logMessage string) {
	persistenceFailure := sessionapp.IsChatPersistenceError(err)
	if persistenceFailure {
		h.logSessionError(logMessage, err)
		writeSessionSSEError(w, "PROCESSING_ERROR", "回复保存失败，请稍后重试")
		return
	}
	if errors.Is(err, context.Canceled) {
		h.logger.Debug("session chat request canceled")
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		writeSessionError(w, http.StatusGatewayTimeout, "REQUEST_TIMEOUT", "请求处理超时，请稍后重试")
		return
	}
	if writeAIRiskSSEError(w, err) {
		return
	}
	switch {
	case errors.Is(err, sessionapp.ErrEmptyMessage):
		writeSessionError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "消息内容不能为空")
	case errors.Is(err, sessionapp.ErrInvalidMode):
		writeSessionError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "不支持的会话模式")
	case errors.Is(err, sessionapp.ErrInvalidSessionID):
		writeSessionError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "会话标识格式错误")
	case errors.Is(err, sessionapp.ErrSessionIDConflict):
		writeSessionError(w, http.StatusConflict, "SESSION_ID_CONFLICT", "会话标识已被其他请求使用")
	case errors.Is(err, sessionapp.ErrFirstChatCannotResume):
		writeSessionError(w, http.StatusConflict, "FIRST_CHAT_NOT_RESUMABLE", "会话历史已发生变化，无法安全补写首次回复")
	case errors.Is(err, sessionapp.ErrStartChatInProgress):
		writeSessionSSEError(w, "FIRST_CHAT_IN_PROGRESS", "首次消息仍在处理中，请稍后重试")
	case errors.Is(err, sessionapp.ErrInvalidAttachment):
		writeSessionError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "附件必须是已上传的图片，且最多上传 5 张")
	case errors.Is(err, sessionapp.ErrMessageTooLarge):
		writeSessionError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", fmt.Sprintf("消息和文档内容合计不能超过 %d KiB", sessionapp.MaxChatMessageKiB))
	case errors.Is(err, sessionapp.ErrNotFound):
		writeSessionSSEError(w, "SESSION_NOT_FOUND", "会话不存在或无权访问")
	default:
		h.logSessionError(logMessage, err)
		writeSessionSSEError(w, "PROCESSING_ERROR", "处理消息时发生错误，请稍后重试")
	}
}

func writeAIRiskSSEError(w http.ResponseWriter, err error) bool {
	code, message, ok := aiRiskSSEError(err)
	if !ok {
		return false
	}
	writeSessionSSEError(w, code, message)
	return true
}

func aiRiskSSEError(err error) (string, string, bool) {
	switch {
	case errors.Is(err, airiskapp.ErrAccessBlocked):
		return "AI_ACCESS_BLOCKED", err.Error(), true
	case errors.Is(err, airiskapp.ErrContentBlocked):
		return "AI_CONTENT_BLOCKED", err.Error(), true
	case errors.Is(err, airiskapp.ErrQuotaExceeded):
		return "AI_DAILY_QUOTA_EXCEEDED", err.Error(), true
	case errors.Is(err, airiskapp.ErrConcurrencyExceeded):
		return "AI_CONCURRENCY_LIMIT", err.Error(), true
	case errors.Is(err, airiskapp.ErrUnavailable):
		return "AI_GUARD_UNAVAILABLE", "AI 风控服务暂不可用，请稍后重试", true
	default:
		return "", "", false
	}
}

func (h *Handler) history(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	limit, ok := parseIntQuery(w, r.URL.Query().Get("limit"), 50, 1, 100, "limit")
	if !ok {
		return
	}
	offset, ok := parseIntQuery(w, r.URL.Query().Get("offset"), 0, 0, 1_000_000, "offset")
	if !ok {
		return
	}
	response, err := h.service.GetHistory(r.Context(), r.PathValue("session_id"), principal.UserID, limit, offset)
	if err != nil {
		if errors.Is(err, sessionapp.ErrNotFound) {
			writeSessionError(w, http.StatusNotFound, "NOT_FOUND", "会话不存在或无权访问")
			return
		}
		h.logSessionError("get session history failed", err)
		writeSessionError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "获取会话历史失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	limit, ok := parseIntQuery(w, r.URL.Query().Get("limit"), 20, 1, 50, "limit")
	if !ok {
		return
	}
	offset, ok := parseIntQuery(w, r.URL.Query().Get("offset"), 0, 0, 1_000_000, "offset")
	if !ok {
		return
	}
	withUserMessages, ok := parseBoolQuery(w, r.URL.Query().Get("with_user_messages"), false, "with_user_messages")
	if !ok {
		return
	}
	response, err := h.service.GetSessions(r.Context(), principal.UserID, limit, offset, withUserMessages)
	if err != nil {
		h.logSessionError("get session list failed", err)
		writeSessionError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "获取会话列表失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) end(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	response, err := h.service.EndSession(r.Context(), r.PathValue("session_id"), principal.UserID)
	if err != nil {
		if errors.Is(err, sessionapp.ErrNotFound) {
			writeSessionError(w, http.StatusNotFound, "NOT_FOUND", "会话不存在或无权访问")
			return
		}
		h.logSessionError("end session failed", err)
		writeSessionError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "结束会话失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) updateMode(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	var request updateModeRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	response, err := h.service.UpdateSessionMode(r.Context(), r.PathValue("session_id"), principal.UserID, request.Mode)
	if err != nil {
		if errors.Is(err, sessionapp.ErrInvalidMode) {
			writeSessionError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "不支持的会话模式")
			return
		}
		if errors.Is(err, sessionapp.ErrNotFound) {
			writeSessionError(w, http.StatusNotFound, "NOT_FOUND", "会话不存在或无权访问")
			return
		}
		h.logSessionError("update session mode failed", err)
		writeSessionError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "更新会话模式失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	response, err := h.service.DeleteSession(r.Context(), r.PathValue("session_id"), principal.UserID)
	if err != nil {
		h.logSessionError("delete session failed", err)
		writeSessionError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "删除会话失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) batchDelete(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	var request batchDeleteRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	response, err := h.service.BatchDeleteSessions(r.Context(), request.SessionIDs, principal.UserID)
	if err != nil {
		h.logSessionError("batch delete sessions failed", err)
		writeSessionError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "批量删除会话失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) cancelTask(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	response, err := h.service.CancelTask(r.Context(), r.PathValue("task_id"), principal.UserID)
	if err != nil {
		h.logSessionError("cancel task failed", err)
		writeSessionError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "任务取消失败")
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) requirePrincipal(w http.ResponseWriter, r *http.Request) (authapp.Principal, bool) {
	return httpauth.RequireBearerAccessContext(w, r, h.auth.DecodeActiveAccessToken, nil, "", writeSessionError)
}

func (h *Handler) logSessionError(message string, err error) {
	h.logger.Error(message, "error", redact.String(err.Error()))
}

func parseIntQuery(w http.ResponseWriter, value string, fallback int, minValue int, maxValue int, name string) (int, bool) {
	parsed, err := httpquery.BoundedInt(value, fallback, minValue, maxValue)
	if err != nil {
		writeSessionError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", name+" 参数超出范围")
		return 0, false
	}
	return parsed, true
}

func parseBoolQuery(w http.ResponseWriter, value string, fallback bool, name string) (bool, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, true
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		writeSessionError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", name+" 必须是布尔值")
		return false, false
	}
	return parsed, true
}

func decodeRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	return httpjson.DecodeStrictOrDetailError(w, r, 1<<20, target)
}

func (s *chatSSEWriter) callbacks() sessionapp.ChatStreamCallbacks {
	return sessionapp.ChatStreamCallbacks{
		OnStart: func(start sessionapp.ChatStreamStart) error {
			return s.writeTask(start.TaskID)
		},
		OnChunk: func(chunk sessionapp.ChatStreamChunk) error {
			return s.writeChunk(chunk.MessageID, chunk.Agent, chunk.Content)
		},
	}
}

func (s *chatSSEWriter) writeTask(taskID string) error {
	if s.taskWritten {
		return s.err
	}
	if err := s.writeEvent("task_info", map[string]string{"task_id": taskID}); err != nil {
		return err
	}
	s.taskWritten = true
	return nil
}

func (s *chatSSEWriter) writeChunk(messageID string, agent string, content string) error {
	if content == "" {
		return nil
	}
	if err := s.writeEvent("message", map[string]any{
		"type":       "chunk",
		"content":    content,
		"agent":      agent,
		"message_id": messageID,
	}); err != nil {
		return err
	}
	s.chunkWritten = true
	return nil
}

func (s *chatSSEWriter) writeResult(result sessionapp.ChatResult) error {
	if !s.taskWritten {
		if err := s.writeTask(result.TaskID); err != nil {
			return err
		}
	}
	if result.Stopped {
		return s.writeEvent("cancelled", map[string]string{
			"type":       "cancelled",
			"task_id":    result.TaskID,
			"message_id": result.MessageID,
		})
	}
	if !s.chunkWritten && result.Content != "" {
		if err := s.writeChunk(result.MessageID, result.Agent, result.Content); err != nil {
			return err
		}
	}
	return s.writeEvent("message", map[string]any{
		"type":       "done",
		"message_id": result.MessageID,
		"agent":      result.Agent,
		"knowledge":  result.Knowledge,
	})
}

func (s *chatSSEWriter) writeEvent(event string, payload any) error {
	if s.err != nil {
		return s.err
	}
	if !s.started {
		prepareSSEHeaders(s.response)
		s.response.WriteHeader(http.StatusOK)
		s.started = true
	}
	if err := writeSSEEventChecked(s.response, event, payload); err != nil {
		s.err = err
		return err
	}
	if err := flushSSEChecked(s.response); err != nil {
		s.err = err
		return err
	}
	return nil
}

func writeSessionSSEError(w http.ResponseWriter, code string, message string) {
	prepareSSEHeaders(w)
	w.WriteHeader(http.StatusOK)
	writeSSEErrorEvent(w, code, message)
	flushSSE(w)
}

func prepareSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
}

func writeSSEErrorEvent(w http.ResponseWriter, code string, message string) {
	writeSSEEvent(w, "error", map[string]string{"type": "error", "code": code, "message": message})
}

func writeSSEEvent(w http.ResponseWriter, event string, payload any) {
	_ = writeSSEEventChecked(w, event, payload)
}

func writeSSEEventChecked(w http.ResponseWriter, event string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw)
	return err
}

func flushSSE(w http.ResponseWriter) {
	_ = flushSSEChecked(w)
}

func flushSSEChecked(w http.ResponseWriter) error {
	return http.NewResponseController(w).Flush()
}

func writeSessionError(w http.ResponseWriter, status int, code, message string) {
	httpjson.WriteDetailError(w, status, code, message)
}
