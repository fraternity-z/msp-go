package session

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	airiskapp "mathstudy/backend/internal/application/airisk"
	uploadapp "mathstudy/backend/internal/application/upload"
	"mathstudy/backend/internal/platform/ptrutil"
	"mathstudy/backend/internal/platform/sliceutil"
	"mathstudy/backend/internal/platform/timefmt"
)

// ErrNotFound is returned when the session is absent or not owned by the user.
var ErrNotFound = errors.New("session not found")

// ErrInvalidAttachment is returned when a chat attachment URL is outside the upload image boundary.
var ErrInvalidAttachment = errors.New("invalid attachment")

// ErrInvalidMode is returned when a session mode is outside the supported set.
var ErrInvalidMode = errors.New("invalid session mode")

// ErrMessageTooLarge is returned when a chat message exceeds the AI input boundary.
var ErrMessageTooLarge = errors.New("chat message exceeds size limit")

// ErrEmptyMessage is returned when a chat message has no visible content.
var ErrEmptyMessage = errors.New("chat message is empty")

// ErrInvalidSessionID is returned when a client-created draft session ID is not a UUID v4.
var ErrInvalidSessionID = errors.New("invalid session id")

// ErrSessionIDConflict is returned when a draft ID is already bound to another request.
var ErrSessionIDConflict = errors.New("session id conflicts with an existing session")

// ErrStartChatInProgress is returned when an idempotent replay reaches a first chat still being processed.
var ErrStartChatInProgress = errors.New("first chat is still being processed")

// ErrFirstChatCannotResume is returned when later messages make it unsafe to
// append a missing first reply out of chronological order.
var ErrFirstChatCannotResume = errors.New("first chat can no longer be resumed")

const (
	stoppedAssistantSuffix     = "\n\n> 已停止生成"
	interruptedAssistantSuffix = "\n\n> 生成已中断"
	interruptedWriteTimeout    = 2 * time.Second
	stoppedTaskRetention       = 5 * time.Minute
)

var errGenerationStopped = errors.New("generation stopped by user")

// Repository is the persistence surface required by session use cases.
type Repository interface {
	CreateSession(context.Context, LearningSession, Message) error
	CreateSessionWithMessages(context.Context, LearningSession, []Message) (bool, error)
	CreateFirstChat(context.Context, LearningSession, []Message, FirstChatRequest) (bool, error)
	GetSession(context.Context, string, string) (LearningSession, bool, error)
	GetFirstChatRequest(context.Context, string) (FirstChatRequest, bool, error)
	ClaimFirstChat(context.Context, string, string, time.Time, time.Time) (bool, error)
	ReleaseFirstChat(context.Context, string, string, time.Time) (bool, error)
	CompleteFirstChat(context.Context, FirstChatCompletion) (bool, error)
	GetMessage(context.Context, string, string) (Message, bool, error)
	InsertMessage(context.Context, Message) error
	InsertMeteredAssistantMessage(context.Context, string, Message, string) error
	ListMessages(context.Context, string, int, int) ([]Message, int, error)
	ListSessions(context.Context, string, int, int, bool) ([]SessionListItem, int, error)
	EndSession(context.Context, string, string, time.Time) (EndState, bool, error)
	UpdateSessionMode(context.Context, string, string, string) (*string, bool, error)
	DeleteSession(context.Context, string, string) (bool, error)
	BatchDeleteSessions(context.Context, []string, string) (int, error)
}

// LearningSession stores one learning session.
type LearningSession struct {
	ID           string
	StudentID    string
	IsActive     bool
	CurrentTopic *string
	Mode         string
	StartedAt    time.Time
	EndedAt      *time.Time
}

// Message stores one session message.
type Message struct {
	ID          string
	SessionID   string
	Role        string
	Content     string
	Agent       *string
	Attachments []string
	Knowledge   *KnowledgeState
	CreatedAt   time.Time
}

// FirstChatRequest stores the immutable identity and processing lease for a
// session's atomic first chat request.
type FirstChatRequest struct {
	SessionID          string
	RequestHash        string
	AssistantMessageID string
	ClaimToken         string
	ClaimExpiresAt     time.Time
	CompletedAt        *time.Time
}

// FirstChatCompletion atomically completes a claimed first reply and its
// optional quota ledger entry.
type FirstChatCompletion struct {
	StudentID  string
	ClaimToken string
	UsageDate  string
	Message    Message
	Metered    bool
}

// SessionListItem stores a session row plus message count.
type SessionListItem struct {
	Session      LearningSession
	MessageCount int
}

// EndState identifies end-session update result.
type EndState string

const (
	// EndStateEnded means the session was active and has just been ended.
	EndStateEnded EndState = "ended"
	// EndStateAlreadyEnded means the session was already inactive.
	EndStateAlreadyEnded EndState = "already_ended"
)

// CreateSessionResponse is the Python-compatible POST /session/start response.
type CreateSessionResponse struct {
	SessionID      string          `json:"session_id"`
	UserID         string          `json:"user_id"`
	Topic          *string         `json:"topic"`
	Mode           string          `json:"mode"`
	Status         string          `json:"status"`
	CreatedAt      string          `json:"created_at"`
	WelcomeMessage MessageResponse `json:"welcome_message"`
}

// MessageResponse stores public message data.
type MessageResponse struct {
	ID          string          `json:"id"`
	Role        string          `json:"role"`
	Content     string          `json:"content"`
	Agent       *string         `json:"agent"`
	Timestamp   string          `json:"timestamp"`
	Attachments []string        `json:"attachments"`
	Knowledge   *KnowledgeState `json:"knowledge,omitempty"`
}

// HistoryResponse is the Python-compatible GET /session/{id}/history response.
type HistoryResponse struct {
	SessionID string            `json:"session_id"`
	Status    string            `json:"status"`
	Mode      string            `json:"mode"`
	Messages  []MessageResponse `json:"messages"`
	Total     int               `json:"total"`
	HasMore   bool              `json:"has_more"`
}

// SessionListResponse is the Python-compatible GET /session/list response.
type SessionListResponse struct {
	Sessions []SessionResponse `json:"sessions"`
	Total    int               `json:"total"`
}

// SessionResponse stores one list row.
type SessionResponse struct {
	SessionID    string  `json:"session_id"`
	UserID       string  `json:"user_id"`
	Topic        *string `json:"topic"`
	Mode         string  `json:"mode"`
	Status       string  `json:"status"`
	StartedAt    string  `json:"started_at"`
	EndedAt      *string `json:"ended_at"`
	MessageCount int     `json:"message_count"`
}

// EndResponse is the Python-compatible POST /session/{id}/end response.
type EndResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

// UpdateModeResponse is the Python-compatible PATCH /session/{id}/mode response.
type UpdateModeResponse struct {
	SessionID string  `json:"session_id"`
	Mode      string  `json:"mode"`
	Topic     *string `json:"topic"`
}

// DeleteResponse is the Python-compatible DELETE /session/{id} response.
type DeleteResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// BatchDeleteResponse is the Python-compatible POST /session/batch-delete response.
type BatchDeleteResponse struct {
	Success      bool   `json:"success"`
	DeletedCount int    `json:"deleted_count"`
	Message      string `json:"message"`
}

// ChatResult stores the immediate SSE fallback response data.
type ChatResult struct {
	TaskID    string
	MessageID string
	Agent     string
	Content   string
	Stopped   bool
	Knowledge *KnowledgeState `json:"knowledge,omitempty"`
}

// ChatAgent generates assistant responses for a learning session.
type ChatAgent interface {
	Generate(context.Context, ChatAgentInput) (ChatAgentOutput, error)
}

// StreamingChatAgent incrementally generates assistant content while retaining
// the complete output for persistence.
type StreamingChatAgent interface {
	Stream(context.Context, ChatAgentInput, ChatAgentChunkHandler) (ChatAgentOutput, error)
}

// ChatAgentInput carries session context into the configured agent runtime.
type ChatAgentInput struct {
	SessionID         string
	StudentID         string
	Message           string
	SystemInstruction string
	Attachments       []string
	History           []Message
	KnowledgeContext  string
}

// ChatAgentOutput stores the generated assistant message.
type ChatAgentOutput struct {
	Agent   string
	Content string
}

// ChatAgentChunk is one incremental assistant output from the configured model.
type ChatAgentChunk struct {
	Agent   string
	Content string
}

// ChatAgentChunkHandler consumes one incremental assistant output.
type ChatAgentChunkHandler func(ChatAgentChunk) error

// ChatStreamStart identifies the task before model output begins.
type ChatStreamStart struct {
	TaskID    string
	MessageID string
	Agent     string
}

// ChatStreamChunk carries one transport-neutral assistant content increment.
type ChatStreamChunk struct {
	MessageID string
	Agent     string
	Content   string
}

// ChatStreamCallbacks lets transports publish task metadata and model output
// without leaking SSE details into the application layer.
type ChatStreamCallbacks struct {
	OnStart func(ChatStreamStart) error
	OnChunk func(ChatStreamChunk) error
}

type chatStreamDeliveryError struct {
	cause error
}

func (e *chatStreamDeliveryError) Error() string {
	return "deliver chat stream: " + e.cause.Error()
}

func (e *chatStreamDeliveryError) Unwrap() error {
	return e.cause
}

// chatPersistenceError keeps a response-write failure distinguishable from
// the context or provider error that may have led to the write attempt.
type chatPersistenceError struct {
	cause error
}

func (e *chatPersistenceError) Error() string {
	return "persist chat response: " + e.cause.Error()
}

func (e *chatPersistenceError) Unwrap() error {
	return e.cause
}

func wrapChatPersistenceError(err error) error {
	if err == nil {
		return nil
	}
	var persistenceErr *chatPersistenceError
	if errors.As(err, &persistenceErr) {
		return err
	}
	return &chatPersistenceError{cause: err}
}

func joinChatCleanupError(primary error, cleanup error) error {
	if cleanup == nil {
		return primary
	}
	return wrapChatPersistenceError(errors.Join(primary, cleanup))
}

// IsChatPersistenceError reports whether a chat response could not be saved.
// HTTP transports use this to retain the diagnostic error even when the
// underlying repository returned a context cancellation or deadline error.
func IsChatPersistenceError(err error) bool {
	var persistenceErr *chatPersistenceError
	return errors.As(err, &persistenceErr)
}

// AIRequestGuard applies student AI access, content, quota, and concurrency rules.
type AIRequestGuard interface {
	Acquire(context.Context, string, string, string, bool) (airiskapp.Lease, error)
}

// CancelTaskResponse is the Python-compatible task cancellation response.
type CancelTaskResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// Service implements session use cases.
type Service struct {
	repo               Repository
	agent              ChatAgent
	knowledgeRetriever KnowledgeRetriever
	guard              AIRequestGuard
	logger             *slog.Logger
	now                func() time.Time
	newID              func() (string, error)
	activeTasksMu      sync.Mutex
	activeTasks        map[string]*activeChatTask
	stoppedTasks       map[string]stoppedChatTask
}

type activeChatTask struct {
	studentID      string
	ctx            context.Context
	cancel         context.CancelCauseFunc
	done           chan struct{}
	acceptingStops bool
	stopRequested  bool
	completionErr  error
}

type stoppedChatTask struct {
	studentID     string
	expiresAt     time.Time
	completionErr error
}

type chatTaskLease struct {
	lease      airiskapp.Lease
	once       sync.Once
	reportOnce sync.Once
	err        error
}

// Option customizes the session service.
type Option func(*Service)

// WithChatAgent enables AI-backed chat generation for session messages.
func WithChatAgent(agent ChatAgent) Option {
	return func(service *Service) {
		service.agent = agent
	}
}

// WithAIRequestGuard enables student AI risk-control checks for chat replies.
func WithAIRequestGuard(guard AIRequestGuard) Option {
	return func(service *Service) {
		service.guard = guard
	}
}

// WithLogger enables structured diagnostics for asynchronous chat cleanup.
func WithLogger(logger *slog.Logger) Option {
	return func(service *Service) {
		if logger != nil {
			service.logger = logger
		}
	}
}

// NewService creates a session service.
func NewService(repo Repository, options ...Option) (*Service, error) {
	if repo == nil {
		return nil, errors.New("session repository is nil")
	}
	service := &Service{
		repo:         repo,
		logger:       slog.Default(),
		now:          time.Now,
		newID:        NewUUID,
		activeTasks:  make(map[string]*activeChatTask),
		stoppedTasks: make(map[string]stoppedChatTask),
	}
	for _, option := range options {
		option(service)
	}
	return service, nil
}

// CreateSession creates a learning session and welcome message.
func (s *Service) CreateSession(ctx context.Context, userID string, topic *string, mode string) (CreateSessionResponse, error) {
	if mode == "" {
		mode = "chat"
	}
	mode, err := validateSessionMode(mode)
	if err != nil {
		return CreateSessionResponse{}, err
	}
	now := s.now()
	sessionID, err := s.newID()
	if err != nil {
		return CreateSessionResponse{}, err
	}
	messageID, err := s.newID()
	if err != nil {
		return CreateSessionResponse{}, err
	}
	agent := "tutor"
	session := LearningSession{
		ID:           sessionID,
		StudentID:    userID,
		IsActive:     true,
		CurrentTopic: topic,
		Mode:         mode,
		StartedAt:    now,
	}
	welcome := Message{
		ID:        messageID,
		SessionID: sessionID,
		Role:      "assistant",
		Content:   welcomeMessage(mode),
		Agent:     &agent,
		CreatedAt: now,
	}
	if err := s.repo.CreateSession(ctx, session, welcome); err != nil {
		return CreateSessionResponse{}, err
	}
	return CreateSessionResponse{
		SessionID: sessionID,
		UserID:    userID,
		Topic:     topic,
		Mode:      mode,
		Status:    "active",
		CreatedAt: timefmt.DateTimeMicros(now),
		WelcomeMessage: MessageResponse{
			ID:          messageID,
			Role:        "assistant",
			Content:     welcome.Content,
			Agent:       &agent,
			Timestamp:   timefmt.DateTimeMicros(now),
			Attachments: []string{},
		},
	}, nil
}

// ProcessChat stores the user message and generates an assistant response.
func (s *Service) ProcessChat(ctx context.Context, sessionID string, userID string, message string, attachments []string, stream ChatStreamCallbacks) (ChatResult, error) {
	if strings.TrimSpace(message) == "" {
		return ChatResult{}, ErrEmptyMessage
	}
	if len(message) > maxChatMessageBytes {
		return ChatResult{}, ErrMessageTooLarge
	}
	attachments, err := normalizeChatAttachments(attachments)
	if err != nil {
		return ChatResult{}, err
	}
	current, ok, err := s.repo.GetSession(ctx, sessionID, userID)
	if err != nil {
		return ChatResult{}, err
	}
	if !ok || !current.IsActive {
		return ChatResult{}, ErrNotFound
	}
	firstChat, hasFirstChat, err := s.repo.GetFirstChatRequest(ctx, sessionID)
	if err != nil {
		return ChatResult{}, err
	}
	if hasFirstChat && firstChat.CompletedAt == nil {
		if err := s.completeExpiredFirstChat(ctx, current, userID, firstChat); err != nil {
			return ChatResult{}, err
		}
	}
	systemInstruction := sessionModeInstruction(current.Mode)
	historyByteBudget, ok := chatHistoryByteBudget(message, systemInstruction, attachments)
	if !ok {
		return ChatResult{}, ErrMessageTooLarge
	}
	history, err := s.recentHistory(ctx, sessionID)
	if err != nil {
		return ChatResult{}, err
	}
	history = selectRecentChatHistory(history, historyByteBudget)
	var taskLease *chatTaskLease
	if s.guard != nil {
		lease, err := s.guard.Acquire(ctx, userID, "session_chat", message, true)
		if err != nil {
			return ChatResult{}, err
		}
		taskLease = &chatTaskLease{lease: lease}
		defer s.releaseChatLease(taskLease, "unregistered")
	}
	ids, err := s.newChatMessageIDs()
	if err != nil {
		return ChatResult{}, err
	}
	userCreatedAt := s.now()
	if err := s.repo.InsertMessage(ctx, Message{
		ID:          ids.UserMessageID,
		SessionID:   sessionID,
		Role:        "user",
		Content:     message,
		Attachments: attachments,
		CreatedAt:   userCreatedAt,
	}); err != nil {
		return ChatResult{}, wrapChatPersistenceError(err)
	}
	return s.completeChat(ctx, current, userID, message, attachments, history, systemInstruction, userCreatedAt, ids, taskLease, stream)
}

func normalizeChatAttachments(attachments []string) ([]string, error) {
	if len(attachments) == 0 {
		return []string{}, nil
	}
	if len(attachments) > 5 {
		return nil, ErrInvalidAttachment
	}
	normalized := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		value := strings.TrimSpace(attachment)
		if !uploadapp.IsSafeImagePath(value) {
			return nil, ErrInvalidAttachment
		}
		normalized = append(normalized, value)
	}
	return normalized, nil
}

func (s *Service) recentHistory(ctx context.Context, sessionID string) ([]Message, error) {
	messages, total, err := s.repo.ListMessages(ctx, sessionID, maxChatHistoryMessages, 0)
	if err != nil {
		return nil, err
	}
	if total <= maxChatHistoryMessages {
		return messages, nil
	}
	messages, _, err = s.repo.ListMessages(ctx, sessionID, maxChatHistoryMessages, total-maxChatHistoryMessages)
	if err != nil {
		return nil, err
	}
	// The page boundary can land on an assistant reply whose user question is
	// just outside the window. Do not send that orphaned reply to the model.
	if len(messages) > 0 && normalizedChatRole(messages[0].Role) == "assistant" {
		messages = messages[1:]
	}
	return messages, nil
}

func (s *Service) generateAssistant(ctx context.Context, input ChatAgentInput, onChunk ChatAgentChunkHandler) (ChatAgentOutput, bool, error) {
	if s.agent == nil {
		output := ChatAgentOutput{
			Agent:   "tutor",
			Content: "智能导师尚未配置；你的消息已保存。请管理员在 AI 模型设置中配置导师智能体，或在后端配置 EINO_ENABLED、EINO_API_KEY 和 EINO_MODEL 后恢复回复。",
		}
		return deliverAssistantOutput(output, false, onChunk)
	}

	emitted := false
	var deliveredContent strings.Builder
	deliver := func(chunk ChatAgentChunk) error {
		if chunk.Content == "" {
			return nil
		}
		if chunk.Agent == "" {
			chunk.Agent = "tutor"
		}
		if onChunk != nil {
			if err := onChunk(chunk); err != nil {
				return wrapChatStreamDeliveryError(err)
			}
		}
		deliveredContent.WriteString(chunk.Content)
		emitted = true
		return nil
	}

	var output ChatAgentOutput
	var err error
	if streamingAgent, ok := s.agent.(StreamingChatAgent); ok && onChunk != nil {
		output, err = streamingAgent.Stream(ctx, input, deliver)
	} else {
		output, err = s.agent.Generate(ctx, input)
	}
	if err != nil {
		if emitted {
			// A streaming provider can return a larger accumulated value after
			// the transport has already failed. Persist only chunks delivered
			// successfully so history cannot outrun the client-visible reply.
			output.Content = deliveredContent.String()
		}
		if output.Agent == "" {
			output.Agent = "tutor"
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return output, false, ctxErr
		}
		return output, false, err
	}
	if output.Agent == "" {
		output.Agent = "tutor"
	}
	if strings.TrimSpace(output.Content) == "" {
		output.Content = "智能导师暂未生成有效回复，请稍后重试。"
		return deliverAssistantOutput(output, false, deliver)
	}
	if !emitted {
		if err := deliver(ChatAgentChunk(output)); err != nil {
			return output, false, err
		}
	} else if streamed := deliveredContent.String(); streamed != output.Content {
		if strings.HasPrefix(output.Content, streamed) {
			if err := deliver(ChatAgentChunk{Agent: output.Agent, Content: output.Content[len(streamed):]}); err != nil {
				return output, false, err
			}
		} else {
			output.Content = streamed + interruptedAssistantSuffix
			if err := deliver(ChatAgentChunk{Agent: output.Agent, Content: interruptedAssistantSuffix}); err != nil {
				return output, false, err
			}
			return output, false, nil
		}
	}
	return output, true, nil
}

func deliverAssistantOutput(output ChatAgentOutput, metered bool, onChunk ChatAgentChunkHandler) (ChatAgentOutput, bool, error) {
	if onChunk != nil && output.Content != "" {
		if err := onChunk(ChatAgentChunk(output)); err != nil {
			return output, false, wrapChatStreamDeliveryError(err)
		}
	}
	return output, metered, nil
}

func wrapChatStreamDeliveryError(err error) error {
	if err == nil {
		return nil
	}
	var deliveryErr *chatStreamDeliveryError
	if errors.As(err, &deliveryErr) {
		return err
	}
	return &chatStreamDeliveryError{cause: err}
}

func releaseAILease(lease airiskapp.Lease) error {
	if lease == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return lease.Release(ctx)
}

func (l *chatTaskLease) release() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		l.err = releaseAILease(l.lease)
	})
	return l.err
}

// GetHistory returns a page of session messages.
func (s *Service) GetHistory(ctx context.Context, sessionID string, userID string, limit int, offset int) (HistoryResponse, error) {
	session, ok, err := s.repo.GetSession(ctx, sessionID, userID)
	if err != nil {
		return HistoryResponse{}, err
	}
	if !ok {
		return HistoryResponse{}, ErrNotFound
	}
	limit = clampInt(limit, 1, 100, 50)
	if offset < 0 {
		offset = 0
	}
	messages, total, err := s.repo.ListMessages(ctx, sessionID, limit, offset)
	if err != nil {
		return HistoryResponse{}, err
	}
	return HistoryResponse{
		SessionID: session.ID,
		Status:    sessionStatus(session.IsActive),
		Mode:      session.Mode,
		Messages:  toMessageResponses(messages),
		Total:     total,
		HasMore:   offset+limit < total,
	}, nil
}

// GetSessions returns the user's session list.
func (s *Service) GetSessions(ctx context.Context, userID string, limit int, offset int, withUserMessages bool) (SessionListResponse, error) {
	limit = clampInt(limit, 1, 50, 20)
	if offset < 0 {
		offset = 0
	}
	rows, total, err := s.repo.ListSessions(ctx, userID, limit, offset, withUserMessages)
	if err != nil {
		return SessionListResponse{}, err
	}
	sessions := make([]SessionResponse, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, toSessionResponse(row))
	}
	return SessionListResponse{Sessions: sessions, Total: total}, nil
}

// EndSession marks a session inactive.
func (s *Service) EndSession(ctx context.Context, sessionID string, userID string) (EndResponse, error) {
	state, ok, err := s.repo.EndSession(ctx, sessionID, userID, s.now())
	if err != nil {
		return EndResponse{}, err
	}
	if !ok {
		return EndResponse{}, ErrNotFound
	}
	if state == EndStateAlreadyEnded {
		return EndResponse{Status: "already_ended", Message: "会话已结束"}, nil
	}
	return EndResponse{Status: "ended", Message: "会话已成功结束"}, nil
}

// UpdateSessionMode changes the session mode without changing its topic.
func (s *Service) UpdateSessionMode(ctx context.Context, sessionID string, userID string, mode string) (UpdateModeResponse, error) {
	mode, err := validateSessionMode(mode)
	if err != nil {
		return UpdateModeResponse{}, err
	}
	topic, ok, err := s.repo.UpdateSessionMode(ctx, sessionID, userID, mode)
	if err != nil {
		return UpdateModeResponse{}, err
	}
	if !ok {
		return UpdateModeResponse{}, ErrNotFound
	}
	return UpdateModeResponse{SessionID: sessionID, Mode: mode, Topic: topic}, nil
}

// DeleteSession deletes one owned session.
func (s *Service) DeleteSession(ctx context.Context, sessionID string, userID string) (DeleteResponse, error) {
	ok, err := s.repo.DeleteSession(ctx, sessionID, userID)
	if err != nil {
		return DeleteResponse{}, err
	}
	if !ok {
		return DeleteResponse{Success: false, Message: "会话不存在或无权删除"}, nil
	}
	return DeleteResponse{Success: true, Message: "会话已删除"}, nil
}

// BatchDeleteSessions deletes owned sessions from the requested list.
func (s *Service) BatchDeleteSessions(ctx context.Context, sessionIDs []string, userID string) (BatchDeleteResponse, error) {
	if len(sessionIDs) == 0 {
		return BatchDeleteResponse{Success: false, DeletedCount: 0, Message: "没有找到可删除的会话"}, nil
	}
	count, err := s.repo.BatchDeleteSessions(ctx, sessionIDs, userID)
	if err != nil {
		return BatchDeleteResponse{}, err
	}
	if count == 0 {
		return BatchDeleteResponse{Success: false, DeletedCount: 0, Message: "没有找到可删除的会话"}, nil
	}
	return BatchDeleteResponse{Success: true, DeletedCount: count, Message: "成功删除 " + strconv.Itoa(count) + " 个会话"}, nil
}

func (s *Service) releaseChatLease(lease *chatTaskLease, taskID string) {
	if lease == nil {
		return
	}
	err := lease.release()
	if err == nil || s.logger == nil {
		return
	}
	lease.reportOnce.Do(func() {
		s.logger.Error("release session chat AI lease failed", "task_id", taskID, "error", err)
	})
}

// CancelTask stops an active task owned by the student and waits for its
// interrupted assistant message to finish persisting.
func (s *Service) CancelTask(ctx context.Context, taskID string, studentID string) (CancelTaskResponse, error) {
	taskID = strings.TrimSpace(taskID)
	studentID = strings.TrimSpace(studentID)

	s.activeTasksMu.Lock()
	s.pruneStoppedChatTasksLocked(s.now())
	task, ok := s.activeTasks[taskID]
	if !ok {
		stopped, stoppedOK := s.stoppedTasks[taskID]
		s.activeTasksMu.Unlock()
		if stoppedOK && stopped.studentID == studentID {
			return cancelTaskResult(stopped.completionErr)
		}
		return CancelTaskResponse{Success: false, Message: "任务不存在或已完成"}, nil
	}
	if task.studentID != studentID {
		s.activeTasksMu.Unlock()
		return CancelTaskResponse{Success: false, Message: "任务不存在或已完成"}, nil
	}
	if task.acceptingStops {
		task.acceptingStops = false
		task.cancel(errGenerationStopped)
		task.stopRequested = errors.Is(context.Cause(task.ctx), errGenerationStopped)
	}
	if !task.stopRequested {
		s.activeTasksMu.Unlock()
		return CancelTaskResponse{Success: false, Message: "任务不存在或已完成"}, nil
	}
	s.activeTasksMu.Unlock()

	select {
	case <-task.done:
		return cancelTaskResult(task.completionErr)
	case <-ctx.Done():
		return CancelTaskResponse{Success: false, Message: "任务停止超时"}, ctx.Err()
	}
}

func cancelTaskResult(err error) (CancelTaskResponse, error) {
	if err != nil {
		return CancelTaskResponse{Success: false, Message: "任务停止失败"}, err
	}
	return CancelTaskResponse{Success: true, Message: "任务已停止"}, nil
}

func (s *Service) pruneStoppedChatTasksLocked(now time.Time) {
	for taskID, task := range s.stoppedTasks {
		if !task.expiresAt.After(now) {
			delete(s.stoppedTasks, taskID)
		}
	}
}

func toMessageResponses(messages []Message) []MessageResponse {
	responses := make([]MessageResponse, 0, len(messages))
	for _, message := range messages {
		responses = append(responses, MessageResponse{
			ID:          message.ID,
			Role:        message.Role,
			Content:     message.Content,
			Agent:       ptrutil.Clone(message.Agent),
			Timestamp:   timefmt.DateTimeMicros(message.CreatedAt),
			Attachments: sliceutil.CloneStrings(message.Attachments),
			Knowledge:   message.Knowledge,
		})
	}
	return responses
}

func toSessionResponse(row SessionListItem) SessionResponse {
	session := row.Session
	return SessionResponse{
		SessionID:    session.ID,
		UserID:       session.StudentID,
		Topic:        ptrutil.Clone(session.CurrentTopic),
		Mode:         session.Mode,
		Status:       sessionStatus(session.IsActive),
		StartedAt:    timefmt.DateTimeMicros(session.StartedAt),
		EndedAt:      timefmt.OptionalDateTimeMicros(session.EndedAt),
		MessageCount: row.MessageCount,
	}
}

func sessionStatus(active bool) string {
	if active {
		return "active"
	}
	return "completed"
}

func validateSessionMode(mode string) (string, error) {
	switch mode {
	case "study", "chat", "practice", "explain":
		return mode, nil
	default:
		return "", ErrInvalidMode
	}
}

func welcomeMessage(mode string) string {
	switch mode {
	case "study":
		return "你好！我是你的 AI 高数学习助手。在学习模式下，我会系统性地引导你学习数学概念，从基础到进阶，确保你理解每个知识点。现在，你想学习什么主题？"
	case "practice":
		return "你好！欢迎进入练习模式！我会根据你的学习进度推荐适合的题目，并在你做题过程中提供实时反馈。准备好开始练习了吗？请告诉我你想练习的知识点。"
	case "explain":
		return "你好！在讲解模式下，我会对数学概念进行深入、详细的讲解，帮助你从本质上理解问题。请告诉我你想深入了解的主题或遇到的困惑。"
	default:
		return "你好！我是你的 AI 高数辅导助手。在聊天模式下，你可以随时问我任何数学问题，我会尽力给你最清晰的解答。有什么想问的吗？"
	}
}

func sessionModeInstruction(mode string) string {
	switch mode {
	case "study":
		return "当前会话处于学习模式。请循序渐进地讲授知识，先确认学生已有基础，再分步骤解释、检查理解并给出下一步学习建议。"
	case "practice":
		return "当前会话处于练习模式。请以练习和即时反馈为主，优先引导学生自己作答，再根据回答提供提示、纠错和巩固题。"
	case "explain":
		return "当前会话处于讲解模式。请深入解释概念、公式来源和推理过程，使用必要的例子与反例，并指出常见误区。"
	default:
		return "当前会话处于聊天模式。请直接、清晰地回答学生的数学问题，并在信息不足时先询问必要条件。"
	}
}

func clampInt(value int, minValue int, maxValue int, fallback int) int {
	if value == 0 {
		value = fallback
	}
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
