package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	sessionapp "mathstudy/backend/internal/application/session"
)

// SessionRepository persists learning sessions and messages in PostgreSQL.
type SessionRepository struct {
	Repository
}

// NewSessionRepository creates a PostgreSQL-backed session repository.
func NewSessionRepository(db Querier) (SessionRepository, error) {
	base, err := NewRepository(db)
	if err != nil {
		return SessionRepository{}, err
	}
	return SessionRepository{Repository: base}, nil
}

// CreateSession inserts a session and its welcome message.
func (r SessionRepository) CreateSession(ctx context.Context, session sessionapp.LearningSession, welcome sessionapp.Message) error {
	created, err := r.CreateSessionWithMessages(ctx, session, []sessionapp.Message{welcome})
	if err != nil {
		return err
	}
	if !created {
		return errors.New("session id already exists")
	}
	return nil
}

// CreateSessionWithMessages atomically inserts a session and its initial messages.
// A false result means the caller-provided session ID already exists; no
// initial messages are inserted in that case.
func (r SessionRepository) CreateSessionWithMessages(ctx context.Context, session sessionapp.LearningSession, messages []sessionapp.Message) (bool, error) {
	created := false
	err := withRepositoryTx(ctx, "session create", r.Repository, func(base Repository) SessionRepository {
		return SessionRepository{Repository: base}
	}, func(current SessionRepository) error {
		inserted, err := current.insertSession(ctx, session)
		if err != nil {
			return err
		}
		if !inserted {
			return nil
		}
		created = true
		for _, message := range messages {
			if err := current.InsertMessage(ctx, message); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return created, nil
}

// CreateFirstChat atomically inserts a session, its initial messages, and the
// immutable request metadata used to claim and replay the first assistant reply.
// A false result means the caller-provided session ID already exists; no other
// rows are inserted in that case.
func (r SessionRepository) CreateFirstChat(
	ctx context.Context,
	session sessionapp.LearningSession,
	messages []sessionapp.Message,
	request sessionapp.FirstChatRequest,
) (bool, error) {
	if request.SessionID != session.ID {
		return false, errors.New("first chat request session id does not match session")
	}
	if len(messages) != 2 {
		return false, fmt.Errorf("first chat creation requires exactly 2 initial messages, got %d", len(messages))
	}
	if messages[0].Role != "assistant" || messages[1].Role != "user" {
		return false, errors.New("first chat initial messages must contain an assistant welcome followed by a user message")
	}
	for _, message := range messages {
		if message.SessionID != session.ID {
			return false, errors.New("first chat initial message session id does not match session")
		}
		if message.ID == request.AssistantMessageID {
			return false, errors.New("first chat reply id conflicts with an initial message")
		}
	}
	if request.CompletedAt != nil {
		return false, errors.New("first chat request must not be completed at creation")
	}
	if !request.ClaimExpiresAt.After(session.StartedAt) {
		return false, errors.New("first chat claim must expire after the session starts")
	}

	created := false
	err := withRepositoryTx(ctx, "first chat create", r.Repository, func(base Repository) SessionRepository {
		return SessionRepository{Repository: base}
	}, func(current SessionRepository) error {
		inserted, err := current.insertSession(ctx, session)
		if err != nil {
			return err
		}
		if !inserted {
			return nil
		}
		for _, message := range messages {
			if err := current.InsertMessage(ctx, message); err != nil {
				return err
			}
		}
		if err := current.insertFirstChatRequest(ctx, request); err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return created, nil
}

func (r SessionRepository) insertSession(ctx context.Context, session sessionapp.LearningSession) (bool, error) {
	tag, err := r.DB().Exec(ctx, `
		INSERT INTO public.learning_sessions (
			id,
			student_id,
			is_active,
			current_topic,
			mode,
			current_content_id,
			contents_attempted,
			concepts_discussed,
			started_at,
			ended_at
		)
		VALUES ($1, $2, $3, $4, $5, NULL, '[]'::json, '[]'::json, $6, NULL)
		ON CONFLICT (id) DO NOTHING`,
		session.ID,
		session.StudentID,
		session.IsActive,
		session.CurrentTopic,
		session.Mode,
		session.StartedAt,
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (r SessionRepository) insertFirstChatRequest(ctx context.Context, request sessionapp.FirstChatRequest) error {
	_, err := r.DB().Exec(ctx, `
		INSERT INTO public.session_first_chat_requests (
			session_id,
			request_hash,
			assistant_message_id,
			claim_token,
			claim_expires_at,
			completed_at
		)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		request.SessionID,
		request.RequestHash,
		request.AssistantMessageID,
		request.ClaimToken,
		request.ClaimExpiresAt,
		request.CompletedAt,
	)
	return err
}

// GetSession returns one owned session.
func (r SessionRepository) GetSession(ctx context.Context, sessionID string, userID string) (sessionapp.LearningSession, bool, error) {
	row := r.DB().QueryRow(ctx, `
		SELECT id, student_id, is_active, current_topic, mode, started_at, ended_at
		FROM public.learning_sessions
		WHERE id = $1 AND student_id = $2`,
		sessionID,
		userID,
	)
	return scanOptionalSession(row)
}

// GetFirstChatRequest returns the immutable request metadata and current claim state.
func (r SessionRepository) GetFirstChatRequest(ctx context.Context, sessionID string) (sessionapp.FirstChatRequest, bool, error) {
	var request sessionapp.FirstChatRequest
	var completedAt pgtype.Timestamp
	err := r.DB().QueryRow(ctx, `
		SELECT session_id, request_hash, assistant_message_id, claim_token, claim_expires_at, completed_at
		FROM public.session_first_chat_requests
		WHERE session_id = $1`,
		sessionID,
	).Scan(
		&request.SessionID,
		&request.RequestHash,
		&request.AssistantMessageID,
		&request.ClaimToken,
		&request.ClaimExpiresAt,
		&completedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sessionapp.FirstChatRequest{}, false, nil
		}
		return sessionapp.FirstChatRequest{}, false, err
	}
	request.CompletedAt = timestampPtr(completedAt)
	return request, true, nil
}

// ClaimFirstChat atomically acquires an incomplete first-chat request whose
// previous claim has expired.
func (r SessionRepository) ClaimFirstChat(
	ctx context.Context,
	sessionID string,
	claimToken string,
	now time.Time,
	expiresAt time.Time,
) (bool, error) {
	if !expiresAt.After(now) {
		return false, errors.New("first chat claim expiry must be after the claim time")
	}
	tag, err := r.DB().Exec(ctx, `
		UPDATE public.session_first_chat_requests
		SET claim_token = $2, claim_expires_at = $4
		WHERE session_id = $1
		  AND completed_at IS NULL
		  AND claim_expires_at <= $3`,
		sessionID,
		claimToken,
		now,
		expiresAt,
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ReleaseFirstChat expires the caller's active claim so a cancelled or failed
// request can be retried immediately. A newer claimant or completed request is
// never modified.
func (r SessionRepository) ReleaseFirstChat(
	ctx context.Context,
	sessionID string,
	claimToken string,
	releasedAt time.Time,
) (bool, error) {
	tag, err := r.DB().Exec(ctx, `
		UPDATE public.session_first_chat_requests
		SET claim_expires_at = $3
		WHERE session_id = $1
		  AND claim_token = $2
		  AND completed_at IS NULL
		  AND claim_expires_at > $3`,
		sessionID,
		claimToken,
		releasedAt,
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// CompleteFirstChat atomically marks the active claim complete and stores its
// assistant reply plus optional quota usage. A false result means the claim is
// stale, missing, already completed, or does not own the supplied message.
func (r SessionRepository) CompleteFirstChat(ctx context.Context, completion sessionapp.FirstChatCompletion) (bool, error) {
	if completion.Message.Role != "assistant" {
		return false, errors.New("first chat completion message must be an assistant message")
	}
	completed := false
	err := withRepositoryTx(ctx, "first chat completion", r.Repository, func(base Repository) SessionRepository {
		return SessionRepository{Repository: base}
	}, func(current SessionRepository) error {
		var lockedStudentID string
		if err := current.DB().QueryRow(ctx, `
			SELECT id
			FROM public.users
			WHERE id = $1
			FOR KEY SHARE`,
			completion.StudentID,
		).Scan(&lockedStudentID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		var lockedSessionID string
		if err := current.DB().QueryRow(ctx, `
			SELECT id
			FROM public.learning_sessions
			WHERE id = $1 AND student_id = $2
			FOR KEY SHARE`,
			completion.Message.SessionID,
			completion.StudentID,
		).Scan(&lockedSessionID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		tag, err := current.DB().Exec(ctx, `
			UPDATE public.session_first_chat_requests
			SET completed_at = $4
			WHERE session_id = $1
			  AND assistant_message_id = $2
			  AND claim_token = $3
			  AND completed_at IS NULL`,
			completion.Message.SessionID,
			completion.Message.ID,
			completion.ClaimToken,
			completion.Message.CreatedAt,
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return nil
		}
		if completion.Metered {
			if err := current.InsertMeteredAssistantMessage(ctx, completion.StudentID, completion.Message, completion.UsageDate); err != nil {
				return err
			}
		} else if err := current.InsertMessage(ctx, completion.Message); err != nil {
			return err
		}
		completed = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return completed, nil
}

// GetMessage returns one exact message from a session.
func (r SessionRepository) GetMessage(ctx context.Context, sessionID string, messageID string) (sessionapp.Message, bool, error) {
	row := r.DB().QueryRow(ctx, `
		SELECT id, session_id, role::text, content, agent_type::text, attachments, created_at, knowledge
		FROM public.session_messages
		WHERE session_id = $1 AND id = $2`,
		sessionID,
		messageID,
	)
	message, err := scanMessage(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sessionapp.Message{}, false, nil
		}
		return sessionapp.Message{}, false, err
	}
	return message, true, nil
}

// InsertMessage inserts one session message.
func (r SessionRepository) InsertMessage(ctx context.Context, message sessionapp.Message) error {
	attachmentsRaw, err := json.Marshal(message.Attachments)
	if err != nil {
		return err
	}
	knowledgeRaw, err := knowledgeToDB(message.Knowledge)
	if err != nil {
		return err
	}
	_, err = r.DB().Exec(ctx, `
		INSERT INTO public.session_messages (
			id,
			session_id,
			role,
			content,
			agent_type,
			attachments,
			related_concept_ids,
			related_content_id,
			created_at,
			knowledge
		)
		VALUES ($1, $2, $3::public.messagerole, $4, $5::public.agenttype, $6::json, '[]'::json, NULL, $7, $8::jsonb)`,
		message.ID,
		message.SessionID,
		roleToDB(message.Role),
		message.Content,
		agentToDB(message.Agent),
		string(attachmentsRaw),
		message.CreatedAt,
		knowledgeRaw,
	)
	return err
}

// InsertMeteredAssistantMessage atomically stores a successful reply and its quota ledger row.
func (r SessionRepository) InsertMeteredAssistantMessage(ctx context.Context, studentID string, message sessionapp.Message, usageDate string) error {
	attachmentsRaw, err := json.Marshal(message.Attachments)
	if err != nil {
		return err
	}
	knowledgeRaw, err := knowledgeToDB(message.Knowledge)
	if err != nil {
		return err
	}
	_, err = r.DB().Exec(ctx, `
		WITH inserted_message AS (
			INSERT INTO public.session_messages (
				id,
				session_id,
				role,
				content,
				agent_type,
				attachments,
				related_concept_ids,
				related_content_id,
				created_at,
				knowledge
			)
			VALUES ($1, $2, $3::public.messagerole, $4, $5::public.agenttype, $6::json, '[]'::json, NULL, $7, $10::jsonb)
			RETURNING id
		)
		INSERT INTO public.student_ai_reply_usage (
			id, student_id, session_id, message_id, usage_date, created_at
		)
		SELECT id, $8, $2, id, $9::date, $7
		FROM inserted_message`,
		message.ID,
		message.SessionID,
		roleToDB(message.Role),
		message.Content,
		agentToDB(message.Agent),
		string(attachmentsRaw),
		message.CreatedAt,
		studentID,
		usageDate,
		knowledgeRaw,
	)
	return err
}

// ListMessages returns session messages in ascending chronological order.
func (r SessionRepository) ListMessages(ctx context.Context, sessionID string, limit int, offset int) ([]sessionapp.Message, int, error) {
	var total int
	if err := r.DB().QueryRow(ctx, `
		SELECT count(id)::int
		FROM public.session_messages
		WHERE session_id = $1`,
		sessionID,
	).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.DB().Query(ctx, `
		SELECT id, session_id, role::text, content, agent_type::text, attachments, created_at, knowledge
		FROM public.session_messages
		WHERE session_id = $1
		ORDER BY created_at ASC, id ASC
		OFFSET $2
		LIMIT $3`,
		sessionID,
		offset,
		limit,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	messages := []sessionapp.Message{}
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, 0, err
		}
		messages = append(messages, message)
	}
	return messages, total, rows.Err()
}

// ListSessions returns sessions with message counts and optionally requires a user message.
func (r SessionRepository) ListSessions(ctx context.Context, userID string, limit int, offset int, withUserMessages bool) ([]sessionapp.SessionListItem, int, error) {
	var total int
	if err := r.DB().QueryRow(ctx, `
		SELECT count(ls.id)::int
		FROM public.learning_sessions ls
		WHERE ls.student_id = $1
		  AND (NOT $2::boolean OR EXISTS (
			SELECT 1
			FROM public.session_messages user_message
			WHERE user_message.session_id = ls.id
			  AND user_message.role = 'USER'::public.messagerole
		  ))`,
		userID,
		withUserMessages,
	).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.DB().Query(ctx, `
		SELECT
			ls.id,
			ls.student_id,
			ls.is_active,
			ls.current_topic,
			ls.mode,
			ls.started_at,
			ls.ended_at,
			coalesce(count(sm.id), 0)::int AS message_count
		FROM public.learning_sessions ls
		LEFT JOIN public.session_messages sm ON sm.session_id = ls.id
		WHERE ls.student_id = $1
		  AND (NOT $4::boolean OR EXISTS (
			SELECT 1
			FROM public.session_messages user_message
			WHERE user_message.session_id = ls.id
			  AND user_message.role = 'USER'::public.messagerole
		  ))
		GROUP BY ls.id, ls.student_id, ls.is_active, ls.current_topic, ls.mode, ls.started_at, ls.ended_at
		ORDER BY ls.started_at DESC, ls.id DESC
		OFFSET $2
		LIMIT $3`,
		userID,
		offset,
		limit,
		withUserMessages,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := []sessionapp.SessionListItem{}
	for rows.Next() {
		session, count, err := scanSessionListItem(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, sessionapp.SessionListItem{Session: session, MessageCount: count})
	}
	return items, total, rows.Err()
}

// EndSession marks a session inactive.
func (r SessionRepository) EndSession(ctx context.Context, sessionID string, userID string, endedAt time.Time) (sessionapp.EndState, bool, error) {
	session, ok, err := r.GetSession(ctx, sessionID, userID)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, nil
	}
	if !session.IsActive {
		return sessionapp.EndStateAlreadyEnded, true, nil
	}
	_, err = r.DB().Exec(ctx, `
		UPDATE public.learning_sessions
		SET is_active = false, ended_at = $3
		WHERE id = $1 AND student_id = $2`,
		sessionID,
		userID,
		endedAt,
	)
	if err != nil {
		return "", false, err
	}
	return sessionapp.EndStateEnded, true, nil
}

// UpdateSessionMode updates the mode while preserving the session topic.
func (r SessionRepository) UpdateSessionMode(ctx context.Context, sessionID string, userID string, mode string) (*string, bool, error) {
	var topic pgtype.Text
	err := r.DB().QueryRow(ctx, `
		UPDATE public.learning_sessions
		SET mode = $3
		WHERE id = $1 AND student_id = $2
		RETURNING current_topic`,
		sessionID,
		userID,
		mode,
	).Scan(&topic)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}
	return textPtr(topic), true, nil
}

// DeleteSession deletes one owned session and its messages.
func (r SessionRepository) DeleteSession(ctx context.Context, sessionID string, userID string) (bool, error) {
	deleted := false
	err := withRepositoryTx(ctx, "session delete", r.Repository, func(base Repository) SessionRepository {
		return SessionRepository{Repository: base}
	}, func(current SessionRepository) error {
		var lockedID string
		if err := current.DB().QueryRow(ctx, `
			SELECT id
			FROM public.learning_sessions
			WHERE id = $1 AND student_id = $2
			FOR UPDATE`,
			sessionID,
			userID,
		).Scan(&lockedID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		if _, err := current.DB().Exec(ctx, `DELETE FROM public.session_messages WHERE session_id = $1`, lockedID); err != nil {
			return err
		}
		tag, err := current.DB().Exec(ctx, `DELETE FROM public.learning_sessions WHERE id = $1 AND student_id = $2`, lockedID, userID)
		if err != nil {
			return err
		}
		deleted = tag.RowsAffected() > 0
		return nil
	})
	if err != nil {
		return false, err
	}
	return deleted, nil
}

// BatchDeleteSessions deletes owned sessions and their messages.
func (r SessionRepository) BatchDeleteSessions(ctx context.Context, sessionIDs []string, userID string) (int, error) {
	if len(sessionIDs) == 0 {
		return 0, nil
	}
	deletedCount := 0
	err := withRepositoryTx(ctx, "session batch delete", r.Repository, func(base Repository) SessionRepository {
		return SessionRepository{Repository: base}
	}, func(current SessionRepository) error {
		validIDs, err := current.lockOwnedSessionIDs(ctx, sessionIDs, userID)
		if err != nil || len(validIDs) == 0 {
			return err
		}
		if _, err := current.DB().Exec(ctx, `DELETE FROM public.session_messages WHERE session_id = ANY($1::varchar[])`, validIDs); err != nil {
			return err
		}
		tag, err := current.DB().Exec(ctx, `DELETE FROM public.learning_sessions WHERE student_id = $1 AND id = ANY($2::varchar[])`, userID, validIDs)
		if err != nil {
			return err
		}
		deletedCount = int(tag.RowsAffected())
		return nil
	})
	if err != nil {
		return 0, err
	}
	return deletedCount, nil
}

func (r SessionRepository) lockOwnedSessionIDs(ctx context.Context, sessionIDs []string, userID string) ([]string, error) {
	rows, err := r.DB().Query(ctx, `
		SELECT id
		FROM public.learning_sessions
		WHERE student_id = $1 AND id = ANY($2::varchar[])
		ORDER BY id
		FOR UPDATE`,
		userID,
		sessionIDs,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	validIDs := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		validIDs = append(validIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return validIDs, nil
}

func scanOptionalSession(row pgx.Row) (sessionapp.LearningSession, bool, error) {
	session, err := scanSession(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return sessionapp.LearningSession{}, false, nil
		}
		return sessionapp.LearningSession{}, false, err
	}
	return session, true, nil
}

func scanSession(scanner rowScanner) (sessionapp.LearningSession, error) {
	var session sessionapp.LearningSession
	var topic pgtype.Text
	var endedAt pgtype.Timestamp
	if err := scanner.Scan(&session.ID, &session.StudentID, &session.IsActive, &topic, &session.Mode, &session.StartedAt, &endedAt); err != nil {
		return sessionapp.LearningSession{}, err
	}
	session.CurrentTopic = textPtr(topic)
	session.EndedAt = timestampPtr(endedAt)
	return session, nil
}

func scanSessionListItem(rows pgx.Rows) (sessionapp.LearningSession, int, error) {
	var session sessionapp.LearningSession
	var topic pgtype.Text
	var endedAt pgtype.Timestamp
	var count int
	if err := rows.Scan(&session.ID, &session.StudentID, &session.IsActive, &topic, &session.Mode, &session.StartedAt, &endedAt, &count); err != nil {
		return sessionapp.LearningSession{}, 0, err
	}
	session.CurrentTopic = textPtr(topic)
	session.EndedAt = timestampPtr(endedAt)
	return session, count, nil
}

func knowledgeToDB(knowledge *sessionapp.KnowledgeState) (any, error) {
	if knowledge == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(knowledge)
	if err != nil {
		return nil, fmt.Errorf("encode message knowledge: %w", err)
	}
	return string(encoded), nil
}

func scanMessage(rows rowScanner) (sessionapp.Message, error) {
	var message sessionapp.Message
	var agent pgtype.Text
	var attachmentsRaw []byte
	var knowledgeRaw []byte
	if err := rows.Scan(&message.ID, &message.SessionID, &message.Role, &message.Content, &agent, &attachmentsRaw, &message.CreatedAt, &knowledgeRaw); err != nil {
		return sessionapp.Message{}, err
	}
	message.Role = roleFromDB(message.Role)
	if agent.Valid {
		value := agentFromDB(agent.String)
		message.Agent = &value
	}
	attachments, err := decodeStringSlice(attachmentsRaw)
	if err != nil {
		return sessionapp.Message{}, fmt.Errorf("decode message attachments: %w", err)
	}
	message.Attachments = attachments
	if len(knowledgeRaw) > 0 {
		if err := json.Unmarshal(knowledgeRaw, &message.Knowledge); err != nil {
			return sessionapp.Message{}, fmt.Errorf("decode message knowledge: %w", err)
		}
	}
	return message, nil
}

func roleToDB(role string) string {
	switch role {
	case "assistant":
		return "ASSISTANT"
	case "system":
		return "SYSTEM"
	default:
		return "USER"
	}
}

func roleFromDB(role string) string {
	switch role {
	case "ASSISTANT":
		return "assistant"
	case "SYSTEM":
		return "system"
	default:
		return "user"
	}
}

func agentToDB(agent *string) any {
	if agent == nil {
		return nil
	}
	switch *agent {
	case "math_solver":
		return "SOLVER"
	case "diagnostician":
		return "DIAGNOSTICIAN"
	case "tutor":
		return "TUTOR"
	default:
		return nil
	}
}

func agentFromDB(agent string) string {
	switch agent {
	case "SOLVER":
		return "math_solver"
	case "DIAGNOSTICIAN":
		return "diagnostician"
	case "TUTOR":
		return "tutor"
	case "PLANNER":
		return "planner"
	default:
		return ""
	}
}
