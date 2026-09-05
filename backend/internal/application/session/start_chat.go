package session

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	airiskapp "mathstudy/backend/internal/application/airisk"
)

// StartChatSession identifies a session after its welcome and first user
// message have committed atomically.
type StartChatSession struct {
	SessionID string `json:"session_id"`
}

// StartChatNotifier runs immediately after the initial session transaction
// commits and before assistant generation begins. Transport notification
// failures must not be reported as persistence failures.
type StartChatNotifier func(StartChatSession)

type chatMessageIDs struct {
	UserMessageID      string
	AssistantMessageID string
	TaskID             string
}

const (
	defaultFirstChatClaimTTL = 3 * time.Minute
	firstChatClaimGrace      = 5 * time.Second
)

// StartChat atomically materializes a draft session and its first user message,
// then generates the assistant reply. The notifier lets transports publish the
// committed session ID before the potentially slow model call.
func (s *Service) StartChat(
	ctx context.Context,
	userID string,
	sessionID string,
	topic *string,
	mode string,
	message string,
	attachments []string,
	onStarted StartChatNotifier,
	stream ChatStreamCallbacks,
) (ChatResult, error) {
	if !isUUIDv4(sessionID) {
		return ChatResult{}, ErrInvalidSessionID
	}
	sessionID = strings.ToLower(sessionID)
	if strings.TrimSpace(message) == "" {
		return ChatResult{}, ErrEmptyMessage
	}
	if len(message) > maxChatMessageBytes {
		return ChatResult{}, ErrMessageTooLarge
	}
	if mode == "" {
		mode = "chat"
	}
	validatedMode, err := validateSessionMode(mode)
	if err != nil {
		return ChatResult{}, err
	}
	attachments, err = normalizeChatAttachments(attachments)
	if err != nil {
		return ChatResult{}, err
	}
	systemInstruction := sessionModeInstruction(validatedMode)
	historyByteBudget, ok := chatHistoryByteBudget(message, systemInstruction, attachments)
	if !ok {
		return ChatResult{}, ErrMessageTooLarge
	}
	requestHash := firstChatRequestHash(topic, validatedMode, message, attachments)
	existing, exists, err := s.repo.GetSession(ctx, sessionID, userID)
	if err != nil {
		return ChatResult{}, err
	}
	if exists {
		return s.replayStartChat(
			ctx,
			userID,
			existing,
			topic,
			validatedMode,
			message,
			attachments,
			historyByteBudget,
			onStarted,
			requestHash,
			nil,
			stream,
		)
	}
	var taskLease *chatTaskLease
	if s.guard != nil {
		lease, err := s.guard.Acquire(ctx, userID, "session_chat", message, true)
		if err != nil {
			return ChatResult{}, err
		}
		taskLease = &chatTaskLease{lease: lease}
		defer s.releaseChatLease(taskLease, "unregistered")
	}

	welcomeMessageID, err := s.newID()
	if err != nil {
		return ChatResult{}, err
	}
	ids, err := s.newChatMessageIDs()
	if err != nil {
		return ChatResult{}, err
	}
	ids.AssistantMessageID = startAssistantMessageID(sessionID)

	startedAt := s.now()
	userCreatedAt := s.now()
	if !userCreatedAt.After(startedAt) {
		userCreatedAt = startedAt.Add(time.Microsecond)
	}
	agent := "tutor"
	session := LearningSession{
		ID:           sessionID,
		StudentID:    userID,
		IsActive:     true,
		CurrentTopic: topic,
		Mode:         validatedMode,
		StartedAt:    startedAt,
	}
	welcome := Message{
		ID:        welcomeMessageID,
		SessionID: sessionID,
		Role:      "assistant",
		Content:   welcomeMessage(validatedMode),
		Agent:     &agent,
		CreatedAt: startedAt,
	}
	userMessage := Message{
		ID:          ids.UserMessageID,
		SessionID:   sessionID,
		Role:        "user",
		Content:     message,
		Attachments: attachments,
		CreatedAt:   userCreatedAt,
	}
	created, err := s.repo.CreateFirstChat(ctx, session, []Message{welcome, userMessage}, FirstChatRequest{
		SessionID:          sessionID,
		RequestHash:        requestHash,
		AssistantMessageID: ids.AssistantMessageID,
		ClaimToken:         ids.TaskID,
		ClaimExpiresAt:     firstChatClaimExpiresAt(ctx, startedAt),
	})
	if err != nil {
		return ChatResult{}, wrapChatPersistenceError(errors.Join(err, s.releaseFirstChatClaim(session.ID, ids.TaskID)))
	}
	if !created {
		existing, exists, err := s.repo.GetSession(ctx, sessionID, userID)
		if err != nil {
			return ChatResult{}, err
		}
		if !exists {
			return ChatResult{}, ErrSessionIDConflict
		}
		return s.replayStartChat(
			ctx,
			userID,
			existing,
			topic,
			validatedMode,
			message,
			attachments,
			historyByteBudget,
			onStarted,
			requestHash,
			taskLease,
			stream,
		)
	}
	if onStarted != nil {
		onStarted(StartChatSession{SessionID: sessionID})
	}

	history := selectRecentChatHistory([]Message{welcome}, historyByteBudget)
	return s.completeFirstChat(
		ctx,
		session,
		userID,
		message,
		attachments,
		history,
		systemInstruction,
		userCreatedAt,
		airiskapp.UsageDate(startedAt),
		ids,
		taskLease,
		stream,
	)
}

func (s *Service) replayStartChat(
	ctx context.Context,
	userID string,
	session LearningSession,
	topic *string,
	mode string,
	message string,
	attachments []string,
	historyByteBudget int,
	onStarted StartChatNotifier,
	requestHash string,
	taskLease *chatTaskLease,
	stream ChatStreamCallbacks,
) (ChatResult, error) {
	storedRequest, exists, err := s.repo.GetFirstChatRequest(ctx, session.ID)
	if err != nil {
		return ChatResult{}, err
	}
	if !exists || storedRequest.RequestHash != requestHash {
		return ChatResult{}, ErrSessionIDConflict
	}
	if onStarted != nil {
		onStarted(StartChatSession{SessionID: session.ID})
	}
	storedReply, replyExists, err := s.repo.GetMessage(ctx, session.ID, storedRequest.AssistantMessageID)
	if err != nil {
		return ChatResult{}, err
	}
	if replyExists {
		return s.publishStoredFirstChat(storedReply, stream)
	}
	if storedRequest.CompletedAt != nil {
		return ChatResult{}, ErrFirstChatCannotResume
	}

	claimTime := s.now()
	if storedRequest.ClaimExpiresAt.After(claimTime) {
		return s.replayLatestFirstChat(ctx, session.ID, stream)
	}

	if taskLease == nil && s.guard != nil {
		lease, err := s.guard.Acquire(ctx, userID, "session_chat", message, true)
		if err != nil {
			return ChatResult{}, err
		}
		taskLease = &chatTaskLease{lease: lease}
		defer s.releaseChatLease(taskLease, "unregistered")
	}
	taskID, err := s.newID()
	if err != nil {
		return ChatResult{}, err
	}
	claimTime = s.now()
	claimed, err := s.repo.ClaimFirstChat(
		ctx,
		session.ID,
		taskID,
		claimTime,
		firstChatClaimExpiresAt(ctx, claimTime),
	)
	if err != nil {
		return ChatResult{}, wrapChatPersistenceError(errors.Join(err, s.releaseFirstChatClaim(session.ID, taskID)))
	}
	if !claimed {
		return s.replayLatestFirstChat(ctx, session.ID, stream)
	}
	messages, total, err := s.repo.ListMessages(ctx, session.ID, 3, 0)
	if err != nil {
		return ChatResult{}, wrapChatPersistenceError(errors.Join(err, s.releaseFirstChatClaim(session.ID, taskID)))
	}
	if total != 2 || len(messages) != 2 ||
		normalizedChatRole(messages[0].Role) != "assistant" ||
		normalizedChatRole(messages[1].Role) != "user" {
		return ChatResult{}, joinChatCleanupError(ErrFirstChatCannotResume, s.releaseFirstChatClaim(session.ID, taskID))
	}
	ids := chatMessageIDs{
		UserMessageID:      messages[1].ID,
		AssistantMessageID: storedRequest.AssistantMessageID,
		TaskID:             taskID,
	}
	history := selectRecentChatHistory([]Message{messages[0]}, historyByteBudget)
	return s.completeFirstChat(
		ctx,
		session,
		userID,
		message,
		attachments,
		history,
		sessionModeInstruction(mode),
		messages[1].CreatedAt,
		airiskapp.UsageDate(claimTime),
		ids,
		taskLease,
		stream,
	)
}

func (s *Service) replayLatestFirstChat(
	ctx context.Context,
	sessionID string,
	stream ChatStreamCallbacks,
) (ChatResult, error) {
	request, exists, err := s.repo.GetFirstChatRequest(ctx, sessionID)
	if err != nil {
		return ChatResult{}, err
	}
	if !exists {
		return ChatResult{}, ErrSessionIDConflict
	}
	if request.CompletedAt == nil {
		return ChatResult{}, ErrStartChatInProgress
	}
	reply, exists, err := s.repo.GetMessage(ctx, sessionID, request.AssistantMessageID)
	if err != nil {
		return ChatResult{}, err
	}
	if !exists {
		return ChatResult{}, ErrFirstChatCannotResume
	}
	return s.publishStoredFirstChat(reply, stream)
}

func (s *Service) publishStoredFirstChat(reply Message, stream ChatStreamCallbacks) (ChatResult, error) {
	taskID, err := s.newID()
	if err != nil {
		return ChatResult{}, err
	}
	agent := "tutor"
	if reply.Agent != nil && strings.TrimSpace(*reply.Agent) != "" {
		agent = *reply.Agent
	}
	result := ChatResult{
		TaskID:    taskID,
		MessageID: reply.ID,
		Agent:     agent,
		Content:   reply.Content,
		Knowledge: reply.Knowledge,
	}
	if err := publishStoredChatResult(stream, result); err != nil {
		return ChatResult{}, err
	}
	return result, nil
}

func (s *Service) completeExpiredFirstChat(
	ctx context.Context,
	session LearningSession,
	studentID string,
	request FirstChatRequest,
) error {
	now := s.now()
	claimToken, err := s.newID()
	if err != nil {
		return err
	}
	claimed, err := s.repo.ClaimFirstChat(
		ctx,
		session.ID,
		claimToken,
		now,
		firstChatClaimExpiresAt(ctx, now),
	)
	if err != nil {
		return wrapChatPersistenceError(err)
	}
	if !claimed {
		return s.resolveFirstChatCompletion(ctx, session.ID, ErrStartChatInProgress)
	}

	messages, total, err := s.repo.ListMessages(ctx, session.ID, 3, 0)
	if err != nil {
		return wrapChatPersistenceError(errors.Join(err, s.releaseFirstChatClaim(session.ID, claimToken)))
	}
	if total != 2 || len(messages) != 2 ||
		normalizedChatRole(messages[0].Role) != "assistant" ||
		normalizedChatRole(messages[1].Role) != "user" {
		if releaseErr := s.releaseFirstChatClaim(session.ID, claimToken); releaseErr != nil {
			return wrapChatPersistenceError(errors.Join(ErrFirstChatCannotResume, releaseErr))
		}
		return s.resolveFirstChatCompletion(ctx, session.ID, ErrFirstChatCannotResume)
	}

	agent := "tutor"
	createdAt := s.now()
	if !createdAt.After(messages[1].CreatedAt) {
		createdAt = messages[1].CreatedAt.Add(time.Microsecond)
	}
	writeCtx, cancel := interruptedWriteContext(ctx)
	completed, completeErr := s.repo.CompleteFirstChat(writeCtx, FirstChatCompletion{
		StudentID:  studentID,
		ClaimToken: claimToken,
		Message: Message{
			ID:        request.AssistantMessageID,
			SessionID: session.ID,
			Role:      "assistant",
			Content:   strings.TrimPrefix(interruptedAssistantSuffix, "\n\n"),
			Agent:     &agent,
			CreatedAt: createdAt,
		},
		Metered: false,
	})
	cancel()
	if completeErr != nil {
		return wrapChatPersistenceError(errors.Join(completeErr, s.releaseFirstChatClaim(session.ID, claimToken)))
	}
	if !completed {
		if releaseErr := s.releaseFirstChatClaim(session.ID, claimToken); releaseErr != nil {
			return wrapChatPersistenceError(errors.Join(ErrStartChatInProgress, releaseErr))
		}
		return s.resolveFirstChatCompletion(ctx, session.ID, ErrStartChatInProgress)
	}
	return nil
}

func (s *Service) resolveFirstChatCompletion(ctx context.Context, sessionID string, fallback error) error {
	latest, exists, err := s.repo.GetFirstChatRequest(ctx, sessionID)
	if err != nil {
		return err
	}
	if exists && latest.CompletedAt != nil {
		return nil
	}
	return fallback
}

func firstChatRequestHash(topic *string, mode string, message string, attachments []string) string {
	payload := struct {
		Version     int      `json:"version"`
		Topic       *string  `json:"topic"`
		Mode        string   `json:"mode"`
		Message     string   `json:"message"`
		Attachments []string `json:"attachments"`
	}{
		Version:     1,
		Topic:       topic,
		Mode:        mode,
		Message:     message,
		Attachments: attachments,
	}
	encoded, _ := json.Marshal(payload)
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

func firstChatClaimExpiresAt(ctx context.Context, now time.Time) time.Time {
	ttl := defaultFirstChatClaimTTL
	if deadline, ok := ctx.Deadline(); ok && deadline.After(now) {
		ttl = deadline.Sub(now) + firstChatClaimGrace
	}
	return now.Add(ttl)
}

func startAssistantMessageID(sessionID string) string {
	digest := sha256.Sum256([]byte("session-first-assistant:" + sessionID))
	digest[6] = (digest[6] & 0x0f) | 0x50
	digest[8] = (digest[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		digest[0:4],
		digest[4:6],
		digest[6:8],
		digest[8:10],
		digest[10:16],
	)
}

func isUUIDv4(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for index := 0; index < len(value); index++ {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		character := value[index]
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	if value[14] != '4' {
		return false
	}
	return strings.ContainsRune("89abAB", rune(value[19]))
}

func (s *Service) newChatMessageIDs() (chatMessageIDs, error) {
	userMessageID, err := s.newID()
	if err != nil {
		return chatMessageIDs{}, err
	}
	assistantMessageID, err := s.newID()
	if err != nil {
		return chatMessageIDs{}, err
	}
	taskID, err := s.newID()
	if err != nil {
		return chatMessageIDs{}, err
	}
	return chatMessageIDs{
		UserMessageID:      userMessageID,
		AssistantMessageID: assistantMessageID,
		TaskID:             taskID,
	}, nil
}

func (s *Service) completeChat(
	ctx context.Context,
	session LearningSession,
	userID string,
	message string,
	attachments []string,
	history []Message,
	systemInstruction string,
	userCreatedAt time.Time,
	ids chatMessageIDs,
	taskLease *chatTaskLease,
	stream ChatStreamCallbacks,
) (ChatResult, error) {
	taskCtx, task, err := s.registerActiveChatTask(ctx, ids.TaskID, userID)
	if err != nil {
		return ChatResult{}, err
	}
	var persistenceErr error
	defer func() {
		s.finishActiveChatTask(ids.TaskID, task, persistenceErr)
	}()
	defer s.releaseChatLease(taskLease, ids.TaskID)

	assistantMessage, agent, metered, generationErr := s.buildAssistantMessage(
		taskCtx,
		session,
		userID,
		message,
		attachments,
		history,
		systemInstruction,
		userCreatedAt,
		ids,
		stream,
	)
	stopped := s.finishActiveChatGeneration(task)
	if generationErr == nil {
		if ctxErr := taskCtx.Err(); ctxErr != nil {
			generationErr = ctxErr
		} else if stopped {
			generationErr = context.Canceled
		}
	}
	if generationErr != nil {
		assistantMessage.Content = interruptedAssistantContent(
			assistantMessage.Content,
			stopped,
		)
		writeCtx, cancel := interruptedWriteContext(taskCtx)
		persistenceErr = s.repo.InsertMessage(writeCtx, assistantMessage)
		cancel()
		if persistenceErr != nil {
			return ChatResult{}, wrapChatPersistenceError(persistenceErr)
		}
		if stopped {
			return stoppedChatResult(ids.TaskID, assistantMessage, agent), nil
		}
		return ChatResult{}, generationErr
	}
	writeCtx, cancel := interruptedWriteContext(taskCtx)
	if metered {
		persistenceErr = s.repo.InsertMeteredAssistantMessage(writeCtx, userID, assistantMessage, airiskapp.UsageDate(userCreatedAt))
	} else {
		persistenceErr = s.repo.InsertMessage(writeCtx, assistantMessage)
	}
	cancel()
	if persistenceErr != nil {
		return ChatResult{}, wrapChatPersistenceError(persistenceErr)
	}
	return chatResult(ids.TaskID, assistantMessage, agent), nil
}

func (s *Service) completeFirstChat(
	ctx context.Context,
	session LearningSession,
	userID string,
	message string,
	attachments []string,
	history []Message,
	systemInstruction string,
	userCreatedAt time.Time,
	usageDate string,
	ids chatMessageIDs,
	taskLease *chatTaskLease,
	stream ChatStreamCallbacks,
) (ChatResult, error) {
	taskCtx, task, err := s.registerActiveChatTask(ctx, ids.TaskID, userID)
	if err != nil {
		return ChatResult{}, joinChatCleanupError(err, s.releaseFirstChatClaim(session.ID, ids.TaskID))
	}
	var persistenceErr error
	defer func() {
		s.finishActiveChatTask(ids.TaskID, task, persistenceErr)
	}()
	defer s.releaseChatLease(taskLease, ids.TaskID)

	assistantMessage, agent, metered, generationErr := s.buildAssistantMessage(
		taskCtx,
		session,
		userID,
		message,
		attachments,
		history,
		systemInstruction,
		userCreatedAt,
		ids,
		stream,
	)
	stopped := s.finishActiveChatGeneration(task)
	if generationErr == nil {
		if ctxErr := taskCtx.Err(); ctxErr != nil {
			generationErr = ctxErr
		} else if stopped {
			generationErr = context.Canceled
		}
	}
	if generationErr != nil {
		assistantMessage.Content = interruptedAssistantContent(
			assistantMessage.Content,
			stopped,
		)
		writeCtx, cancel := interruptedWriteContext(taskCtx)
		completed, completeErr := s.repo.CompleteFirstChat(writeCtx, FirstChatCompletion{
			StudentID:  userID,
			ClaimToken: ids.TaskID,
			Message:    assistantMessage,
			Metered:    false,
		})
		cancel()
		if completeErr != nil {
			persistenceErr = completeErr
			return ChatResult{}, wrapChatPersistenceError(errors.Join(completeErr, s.releaseFirstChatClaim(session.ID, ids.TaskID)))
		}
		if !completed {
			persistenceErr = ErrStartChatInProgress
			return ChatResult{}, persistenceErr
		}
		if stopped {
			return stoppedChatResult(ids.TaskID, assistantMessage, agent), nil
		}
		return ChatResult{}, generationErr
	}
	writeCtx, cancel := interruptedWriteContext(taskCtx)
	completed, completeErr := s.repo.CompleteFirstChat(writeCtx, FirstChatCompletion{
		StudentID:  userID,
		ClaimToken: ids.TaskID,
		UsageDate:  usageDate,
		Message:    assistantMessage,
		Metered:    metered,
	})
	cancel()
	if completeErr != nil {
		persistenceErr = completeErr
		return ChatResult{}, wrapChatPersistenceError(errors.Join(completeErr, s.releaseFirstChatClaim(session.ID, ids.TaskID)))
	}
	if !completed {
		persistenceErr = ErrStartChatInProgress
		return ChatResult{}, persistenceErr
	}
	return chatResult(ids.TaskID, assistantMessage, agent), nil
}

func (s *Service) releaseFirstChatClaim(sessionID string, claimToken string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := s.repo.ReleaseFirstChat(ctx, sessionID, claimToken, s.now())
	return err
}

func (s *Service) buildAssistantMessage(
	ctx context.Context,
	session LearningSession,
	userID string,
	message string,
	attachments []string,
	history []Message,
	systemInstruction string,
	userCreatedAt time.Time,
	ids chatMessageIDs,
	stream ChatStreamCallbacks,
) (Message, string, bool, error) {
	var generationErr error
	if stream.OnStart != nil {
		if err := stream.OnStart(ChatStreamStart{
			TaskID:    ids.TaskID,
			MessageID: ids.AssistantMessageID,
			Agent:     "tutor",
		}); err != nil {
			generationErr = wrapChatStreamDeliveryError(err)
		}
	}
	var output ChatAgentOutput
	var metered bool
	knowledge := emptyKnowledgeState()
	knowledgeContext := ""
	if generationErr == nil {
		budget, valid := chatHistoryByteBudget(message, systemInstruction, attachments)
		if !valid {
			generationErr = ErrMessageTooLarge
		} else {
			knowledgeContext, knowledge, generationErr = s.prepareChatKnowledge(ctx, userID, message, min(budget, 8<<10))
			history = selectRecentChatHistory(history, budget-len(knowledgeContext))
		}
	}
	if generationErr == nil {
		output, metered, generationErr = s.generateAssistant(ctx, ChatAgentInput{
			SessionID:         session.ID,
			StudentID:         userID,
			Message:           message,
			SystemInstruction: systemInstruction,
			Attachments:       attachments,
			History:           history,
			KnowledgeContext:  knowledgeContext,
		}, func(chunk ChatAgentChunk) error {
			if stream.OnChunk == nil {
				return nil
			}
			agent := chunk.Agent
			if agent == "" {
				agent = "tutor"
			}
			return stream.OnChunk(ChatStreamChunk{
				MessageID: ids.AssistantMessageID,
				Agent:     agent,
				Content:   chunk.Content,
			})
		})
	}
	agent := output.Agent
	if agent == "" {
		agent = "tutor"
	}
	assistantCreatedAt := s.now()
	if !assistantCreatedAt.After(userCreatedAt) {
		assistantCreatedAt = userCreatedAt.Add(time.Microsecond)
	}
	assistantMessage := Message{
		ID:        ids.AssistantMessageID,
		SessionID: session.ID,
		Role:      "assistant",
		Content:   output.Content,
		Agent:     &agent,
		CreatedAt: assistantCreatedAt,
		Knowledge: knowledge,
	}
	return assistantMessage, agent, metered, generationErr
}

func (s *Service) registerActiveChatTask(ctx context.Context, taskID string, studentID string) (context.Context, *activeChatTask, error) {
	taskCtx, cancel := context.WithCancelCause(ctx)
	task := &activeChatTask{
		studentID:      studentID,
		ctx:            taskCtx,
		cancel:         cancel,
		done:           make(chan struct{}),
		acceptingStops: true,
	}

	s.activeTasksMu.Lock()
	defer s.activeTasksMu.Unlock()
	if s.activeTasks == nil {
		s.activeTasks = make(map[string]*activeChatTask)
	}
	if s.stoppedTasks == nil {
		s.stoppedTasks = make(map[string]stoppedChatTask)
	}
	s.pruneStoppedChatTasksLocked(s.now())
	if _, exists := s.activeTasks[taskID]; exists {
		cancel(errors.New("duplicate active chat task"))
		return nil, nil, errors.New("chat task id is already active")
	}
	if _, exists := s.stoppedTasks[taskID]; exists {
		cancel(errors.New("duplicate stopped chat task"))
		return nil, nil, errors.New("chat task id was recently stopped")
	}
	s.activeTasks[taskID] = task
	return taskCtx, task, nil
}

func (s *Service) finishActiveChatGeneration(task *activeChatTask) bool {
	s.activeTasksMu.Lock()
	defer s.activeTasksMu.Unlock()
	task.acceptingStops = false
	task.stopRequested = errors.Is(context.Cause(task.ctx), errGenerationStopped)
	return task.stopRequested
}

func (s *Service) finishActiveChatTask(taskID string, task *activeChatTask, completionErr error) {
	s.activeTasksMu.Lock()
	defer s.activeTasksMu.Unlock()
	task.cancel(nil)
	task.stopRequested = errors.Is(context.Cause(task.ctx), errGenerationStopped)
	task.completionErr = completionErr
	if current, exists := s.activeTasks[taskID]; exists && current == task {
		delete(s.activeTasks, taskID)
	}
	if task.stopRequested {
		if s.stoppedTasks == nil {
			s.stoppedTasks = make(map[string]stoppedChatTask)
		}
		s.stoppedTasks[taskID] = stoppedChatTask{
			studentID:     task.studentID,
			expiresAt:     s.now().Add(stoppedTaskRetention),
			completionErr: completionErr,
		}
	}
	close(task.done)
}

func interruptedAssistantContent(content string, stopped bool) string {
	suffix := interruptedAssistantSuffix
	if stopped {
		suffix = stoppedAssistantSuffix
	}
	if strings.TrimSpace(content) == "" {
		return strings.TrimPrefix(suffix, "\n\n")
	}
	if strings.HasSuffix(content, suffix) {
		return content
	}
	return content + suffix
}

func interruptedWriteContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), interruptedWriteTimeout)
}

func publishStoredChatResult(stream ChatStreamCallbacks, result ChatResult) error {
	if stream.OnStart != nil {
		if err := stream.OnStart(ChatStreamStart{
			TaskID:    result.TaskID,
			MessageID: result.MessageID,
			Agent:     result.Agent,
		}); err != nil {
			return wrapChatStreamDeliveryError(err)
		}
	}
	if stream.OnChunk != nil && result.Content != "" {
		if err := stream.OnChunk(ChatStreamChunk{
			MessageID: result.MessageID,
			Agent:     result.Agent,
			Content:   result.Content,
		}); err != nil {
			return wrapChatStreamDeliveryError(err)
		}
	}
	return nil
}

func chatResult(taskID string, message Message, agent string) ChatResult {
	return ChatResult{
		TaskID:    taskID,
		MessageID: message.ID,
		Agent:     agent,
		Content:   message.Content,
		Knowledge: message.Knowledge,
	}
}

func stoppedChatResult(taskID string, message Message, agent string) ChatResult {
	result := chatResult(taskID, message, agent)
	result.Stopped = true
	return result
}
