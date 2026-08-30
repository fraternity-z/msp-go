package postgres

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	conversationapp "mathstudy/backend/internal/application/conversation"
	"mathstudy/backend/internal/application/messageattachment"
	wechatreminder "mathstudy/backend/internal/application/wechatreminder"
	"mathstudy/backend/internal/domain/user"
)

// ConversationRepository persists conversation data in PostgreSQL.
type ConversationRepository struct {
	Repository
	wechatReminders WechatReminderEnqueuer
}

// NewConversationRepository creates a PostgreSQL-backed conversation repository.
func NewConversationRepository(db Querier, reminderEnqueuer WechatReminderEnqueuer) (ConversationRepository, error) {
	base, err := NewRepository(db)
	if err != nil {
		return ConversationRepository{}, err
	}
	return ConversationRepository{Repository: base, wechatReminders: reminderEnqueuer}, nil
}

// ListConversations returns paginated conversations for a user.
func (r ConversationRepository) ListConversations(ctx context.Context, userID string, role user.Role, search string, status string, className string, page int, pageSize int) ([]conversationapp.ConversationItem, int, error) {
	pgPage, err := NewPage((page-1)*pageSize, pageSize)
	if err != nil {
		return nil, 0, err
	}

	var total int
	var items []conversationapp.ConversationItem

	if role == user.RoleStudent {
		items, total, err = r.listStudentConversations(ctx, userID, search, pgPage)
	} else {
		items, total, err = r.listTeacherConversations(ctx, userID, search, status, className, pgPage)
	}
	return items, total, err
}

func (r ConversationRepository) listStudentConversations(ctx context.Context, studentID string, search string, page Page) ([]conversationapp.ConversationItem, int, error) {
	args := []any{studentID}
	searchFilter := ""
	for _, term := range strings.Fields(search) {
		idx := idxStr(len(args) + 1)
		searchFilter += ` AND (
			STRPOS(LOWER(COALESCE(u.display_name, u.username)), LOWER($` + idx + `)) > 0
			OR STRPOS(LOWER(c.subject), LOWER($` + idx + `)) > 0
		)`
		args = append(args, term)
	}

	var total int
	if err := r.DB().QueryRow(ctx, `
		SELECT COUNT(*)
		FROM public.conversations c
		JOIN public.users u ON u.id = c.teacher_id
		WHERE c.student_id = $1 AND c.student_archived = false`+searchFilter,
		args...,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	countArgs := len(args)
	args = append(args, page.Limit, page.Offset)

	rows, err := r.DB().Query(ctx, `
		SELECT c.id, c.teacher_id, u.display_name, u.username, c.subject, c.last_message_at, c.student_archived,
			COALESCE(cnv.unread_count, 0),
			(SELECT LEFT(CASE WHEN text <> '' THEN text WHEN jsonb_array_length(attachments) > 0 THEN '[附件]' ELSE '' END, 200) FROM public.conversation_messages WHERE conversation_id = c.id ORDER BY created_at DESC, id DESC LIMIT 1)
		FROM public.conversations c
		JOIN public.users u ON u.id = c.teacher_id
		LEFT JOIN LATERAL (
			SELECT COUNT(*) AS unread_count
			FROM public.conversation_messages cm
			WHERE cm.conversation_id = c.id AND cm.sender_role = 'teacher' AND cm.read_at IS NULL
		) cnv ON true
		WHERE c.student_id = $1 AND c.student_archived = false`+searchFilter+`
		ORDER BY c.last_message_at DESC, c.id DESC
		LIMIT $`+idxStr(countArgs+1)+` OFFSET $`+idxStr(countArgs+2),
		args...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]conversationapp.ConversationItem, 0)
	for rows.Next() {
		var item conversationapp.ConversationItem
		var teacherName, teacherUsername, subject string
		var displayName, lastMsg pgtype.Text
		var unread int
		if err := rows.Scan(&item.ID, &item.TeacherID, &displayName, &teacherUsername, &subject, &item.LastTime, &item.Archived, &unread, &lastMsg); err != nil {
			return nil, 0, err
		}
		item.LastTime = messageCenterWallTime(item.LastTime)
		if displayName.Valid {
			teacherName = displayName.String
		} else {
			teacherName = teacherUsername
		}
		item.TeacherName = teacherName
		item.Scope = subject
		if lastMsg.Valid {
			item.LastMessage = lastMsg.String
		}
		item.Unread = unread
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (r ConversationRepository) listTeacherConversations(ctx context.Context, teacherID string, search string, status string, className string, page Page) ([]conversationapp.ConversationItem, int, error) {
	args := []any{teacherID}
	whereIdx := 2
	searchFilter := ""
	for _, term := range strings.Fields(search) {
		idx := idxStr(whereIdx)
		searchFilter += ` AND (
			STRPOS(LOWER(COALESCE(u.display_name, u.username)), LOWER($` + idx + `)) > 0
			OR STRPOS(LOWER(c.subject), LOWER($` + idx + `)) > 0
		)`
		args = append(args, term)
		whereIdx++
	}
	if strings.TrimSpace(className) != "" {
		searchFilter += ` AND STRPOS(LOWER(c.subject), LOWER($` + idxStr(whereIdx) + `)) > 0`
		args = append(args, className)
	}
	switch status {
	case "未读":
		searchFilter += ` AND EXISTS (
			SELECT 1 FROM public.conversation_messages cm
			WHERE cm.conversation_id = c.id AND cm.sender_role = 'student' AND cm.read_at IS NULL
		)`
	case "待回复":
		searchFilter += ` AND (
			SELECT cm.sender_role = 'student'
			FROM public.conversation_messages cm
			WHERE cm.conversation_id = c.id
			ORDER BY cm.created_at DESC, cm.id DESC
			LIMIT 1
		)`
	}

	var total int
	if err := r.DB().QueryRow(ctx, `
		SELECT COUNT(*)
		FROM public.conversations c
		JOIN public.users u ON u.id = c.student_id
		WHERE c.teacher_id = $1 AND c.teacher_archived = false`+searchFilter,
		args...,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	countArgs := len(args)
	args = append(args, page.Limit, page.Offset)

	rows, err := r.DB().Query(ctx, `
		SELECT c.id, c.student_id, u.display_name, u.username, c.subject, c.last_message_at,
			(SELECT LEFT(CASE WHEN text <> '' THEN text WHEN jsonb_array_length(attachments) > 0 THEN '[附件]' ELSE '' END, 200) FROM public.conversation_messages WHERE conversation_id = c.id ORDER BY created_at DESC, id DESC LIMIT 1),
			EXISTS(SELECT 1 FROM public.conversation_messages cm WHERE cm.conversation_id = c.id AND cm.sender_role = 'student' AND cm.read_at IS NULL) AS unread,
			COALESCE((SELECT cm2.sender_role = 'student' FROM public.conversation_messages cm2 WHERE cm2.conversation_id = c.id ORDER BY cm2.created_at DESC, cm2.id DESC LIMIT 1), false) AS pending_reply
		FROM public.conversations c
		JOIN public.users u ON u.id = c.student_id
		WHERE c.teacher_id = $1 AND c.teacher_archived = false`+searchFilter+`
		ORDER BY c.last_message_at DESC, c.id DESC
		LIMIT $`+idxStr(countArgs+1)+` OFFSET $`+idxStr(countArgs+2),
		args...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]conversationapp.ConversationItem, 0)
	for rows.Next() {
		var item conversationapp.ConversationItem
		var studentName, studentUsername, subject string
		var displayName, lastMsg pgtype.Text
		var unread, pendingReply bool
		if err := rows.Scan(&item.ID, &item.StudentID, &displayName, &studentUsername, &subject, &item.LastTime, &lastMsg, &unread, &pendingReply); err != nil {
			return nil, 0, err
		}
		item.LastTime = messageCenterWallTime(item.LastTime)
		if displayName.Valid {
			studentName = displayName.String
		} else {
			studentName = studentUsername
		}
		item.StudentName = studentName
		item.ClassName = subject
		if lastMsg.Valid {
			item.LastMessage = lastMsg.String
		}
		if unread {
			item.Unread = 1
		}
		item.PendingReply = pendingReply
		items = append(items, item)
	}
	return items, total, rows.Err()
}

// GetConversation returns a conversation with full message history.
func (r ConversationRepository) GetConversation(ctx context.Context, conversationID string, userID string, page, pageSize int) (conversationapp.ConversationDetail, bool, error) {
	var detail conversationapp.ConversationDetail
	var teacherName, teacherUsername, studentName, studentUsername, subject string
	var teacherDisplay, studentDisplay pgtype.Text
	err := r.DB().QueryRow(ctx, `
		SELECT c.id, c.subject, c.last_message_at,
			CASE WHEN c.student_id = $2 THEN c.student_archived ELSE c.teacher_archived END,
			t.display_name, t.username,
			s.display_name, s.username
		FROM public.conversations c
		JOIN public.users t ON t.id = c.teacher_id
		JOIN public.users s ON s.id = c.student_id
		WHERE c.id = $1 AND (c.student_id = $2 OR c.teacher_id = $2)`,
		conversationID, userID,
	).Scan(&detail.ID, &subject, &detail.LastTime, &detail.Archived,
		&teacherDisplay, &teacherUsername,
		&studentDisplay, &studentUsername)
	if err != nil {
		if err == pgx.ErrNoRows {
			return conversationapp.ConversationDetail{}, false, nil
		}
		return conversationapp.ConversationDetail{}, false, err
	}
	detail.LastTime = messageCenterWallTime(detail.LastTime)
	if teacherDisplay.Valid {
		teacherName = teacherDisplay.String
	} else {
		teacherName = teacherUsername
	}
	if studentDisplay.Valid {
		studentName = studentDisplay.String
	} else {
		studentName = studentUsername
	}
	detail.TeacherName = teacherName
	detail.StudentName = studentName
	detail.Scope = subject
	detail.ClassName = subject

	if page < 1 {
		page = 1
	}
	pgPage, err := NewPage((page-1)*pageSize, pageSize)
	if err != nil {
		return conversationapp.ConversationDetail{}, false, err
	}
	if err := r.DB().QueryRow(ctx, `SELECT COUNT(*) FROM public.conversation_messages WHERE conversation_id = $1`, conversationID).Scan(&detail.MessagesTotal); err != nil {
		return conversationapp.ConversationDetail{}, false, err
	}
	detail.MessagesPage, detail.MessagesSize = page, pgPage.Limit
	// Load the newest page, then restore chronological order for the UI.
	msgRows, err := r.DB().Query(ctx, `
		SELECT cm.id, cm.sender_role, cm.text, cm.created_at, cm.read_at, cm.attachments
		FROM public.conversation_messages cm
		WHERE cm.conversation_id = $1
		ORDER BY cm.created_at DESC, cm.id DESC
		LIMIT $2 OFFSET $3`,
		conversationID, pgPage.Limit, pgPage.Offset,
	)
	if err != nil {
		return conversationapp.ConversationDetail{}, false, err
	}
	defer msgRows.Close()

	detail.Messages = make([]conversationapp.Message, 0)
	for msgRows.Next() {
		var msg conversationapp.Message
		var readAt pgtype.Timestamp
		var attachmentsJSON []byte
		if err := msgRows.Scan(&msg.ID, &msg.From, &msg.Text, &msg.Time, &readAt, &attachmentsJSON); err != nil {
			return conversationapp.ConversationDetail{}, false, err
		}
		msg.Attachments, err = messageattachment.Decode(attachmentsJSON)
		if err != nil {
			return conversationapp.ConversationDetail{}, false, err
		}
		msg.Time = messageCenterWallTime(msg.Time)
		if readAt.Valid {
			b := true
			msg.ReadByRecipient = &b
		}
		detail.Messages = append(detail.Messages, msg)
	}
	if err := msgRows.Err(); err != nil {
		return conversationapp.ConversationDetail{}, false, err
	}
	for left, right := 0, len(detail.Messages)-1; left < right; left, right = left+1, right-1 {
		detail.Messages[left], detail.Messages[right] = detail.Messages[right], detail.Messages[left]
	}
	if len(detail.Messages) > 0 {
		detail.ReadThroughMessageID = detail.Messages[len(detail.Messages)-1].ID
	}

	return detail, true, nil
}

// AcknowledgeConversationRead marks incoming messages no newer than a delivered cutoff.
func (r ConversationRepository) AcknowledgeConversationRead(ctx context.Context, conversationID string, userID string, throughMessageID string) (bool, error) {
	var valid bool
	var updated int
	err := r.DB().QueryRow(ctx, `
		WITH authorized_cutoff AS (
			SELECT cm.created_at, cm.id
			FROM public.conversation_messages cm
			JOIN public.conversations c ON c.id = cm.conversation_id
			WHERE cm.conversation_id = $1
			  AND cm.id = $3
			  AND (c.student_id = $2 OR c.teacher_id = $2)
		), updated AS (
			UPDATE public.conversation_messages target
			SET read_at = clock_timestamp() AT TIME ZONE 'Asia/Shanghai'
			FROM authorized_cutoff cutoff
			WHERE target.conversation_id = $1
			  AND target.sender_id != $2
			  AND target.read_at IS NULL
			  AND (target.created_at, target.id) <= (cutoff.created_at, cutoff.id)
			RETURNING 1
		)
		SELECT EXISTS (SELECT 1 FROM authorized_cutoff), COUNT(*) FROM updated`,
		conversationID, userID, throughMessageID,
	).Scan(&valid, &updated)
	return valid, err
}

// CreateConversation creates a conversation and its first message.
func (r ConversationRepository) CreateConversation(ctx context.Context, creatorID string, creatorRole user.Role, targetID string, subject string, initialMessage string, attachments []messageattachment.Attachment) (conversationapp.ConversationDetail, error) {
	if creatorRole != user.RoleStudent && creatorRole != user.RoleTeacher {
		return conversationapp.ConversationDetail{}, conversationapp.ErrForbidden
	}
	studentID, teacherID := creatorID, targetID
	if creatorRole != user.RoleStudent {
		studentID, teacherID = targetID, creatorID
	}
	convID, err := newUUID()
	if err != nil {
		return conversationapp.ConversationDetail{}, err
	}
	attachmentsJSON, err := messageattachment.Encode(attachments)
	if err != nil {
		return conversationapp.ConversationDetail{}, err
	}

	tx, err := r.beginTx(ctx)
	if err != nil {
		return conversationapp.ConversationDetail{}, err
	}
	defer tx.Rollback(ctx)

	var lockedUserCount int
	err = tx.QueryRow(ctx, `
		WITH locked_users AS MATERIALIZED (
			SELECT u.id
			FROM public.users u
			WHERE u.is_active = true
			  AND (
				(u.id = $1 AND u.role::text = 'STUDENT')
				OR (u.id = $2 AND u.role::text = 'TEACHER')
			  )
			ORDER BY u.id
			FOR UPDATE
		)
		SELECT count(*) FROM locked_users`, studentID, teacherID).Scan(&lockedUserCount)
	if err != nil {
		return conversationapp.ConversationDetail{}, err
	}
	if lockedUserCount != 2 {
		return conversationapp.ConversationDetail{}, conversationapp.ErrForbidden
	}

	hasInitialMessage := strings.TrimSpace(initialMessage) != "" || len(attachments) > 0
	archiveColumn := "student_archived"
	if creatorRole == user.RoleTeacher {
		archiveColumn = "teacher_archived"
	}

	var parentLastMessageAt time.Time
	var studentArchived bool
	var teacherArchived bool
	reopened := true
	err = tx.QueryRow(ctx, `
		SELECT c.id, c.last_message_at, c.student_archived, c.teacher_archived
		FROM public.conversations c
		WHERE c.student_id = $1 AND c.teacher_id = $2
		FOR UPDATE`, studentID, teacherID).Scan(
		&convID,
		&parentLastMessageAt,
		&studentArchived,
		&teacherArchived,
	)
	if err == pgx.ErrNoRows {
		reopened = false
	} else if err != nil {
		return conversationapp.ConversationDetail{}, err
	}
	if reopened {
		creatorArchived := studentArchived
		if creatorRole == user.RoleTeacher {
			creatorArchived = teacherArchived
		}
		if !creatorArchived {
			return conversationapp.ConversationDetail{}, conversationapp.ErrConflict
		}
	}

	if !reopened {
		err = tx.QueryRow(ctx, `
			WITH stamped AS (
				SELECT clock_timestamp() AT TIME ZONE 'Asia/Shanghai' AS created_at
			)
			INSERT INTO public.conversations (
				id, student_id, teacher_id, subject,
				last_message_at, created_at, updated_at
			)
			SELECT $1, $2, $3, $4, stamped.created_at, stamped.created_at, stamped.created_at
			FROM stamped
			RETURNING last_message_at`,
			convID, studentID, teacherID, subject,
		).Scan(&parentLastMessageAt)
		if err != nil {
			if isUniqueViolation(err) {
				return conversationapp.ConversationDetail{}, conversationapp.ErrConflict
			}
			return conversationapp.ConversationDetail{}, err
		}
	}

	if hasInitialMessage {
		msgID, err := newUUID()
		if err != nil {
			return conversationapp.ConversationDetail{}, err
		}
		var messageAt time.Time
		err = tx.QueryRow(ctx, `
			INSERT INTO public.conversation_messages (id, conversation_id, sender_id, sender_role, text, attachments, created_at)
			VALUES (
				$1, $2, $3, $4, $5, $6,
				GREATEST(
					clock_timestamp() AT TIME ZONE 'Asia/Shanghai',
					$7::timestamp without time zone + interval '1 microsecond'
				)
			)
			RETURNING created_at`,
			msgID, convID, creatorID, string(creatorRole), initialMessage, string(attachmentsJSON), parentLastMessageAt,
		).Scan(&messageAt)
		if err != nil {
			return conversationapp.ConversationDetail{}, err
		}
		_, err = tx.Exec(ctx, `
			UPDATE public.conversations
			SET student_archived = false,
				teacher_archived = false,
				subject = CASE WHEN $2 = '' THEN subject ELSE $2 END,
				last_message_at = $3,
				updated_at = $3
			WHERE id = $1`, convID, subject, messageAt)
		if err != nil {
			return conversationapp.ConversationDetail{}, err
		}
		recipientID := teacherID
		if creatorRole == user.RoleTeacher {
			recipientID = studentID
		}
		if err := r.wechatReminders.Enqueue(
			ctx,
			tx,
			wechatreminder.EventPrivateMessage,
			msgID,
			recipientID,
		); err != nil {
			return conversationapp.ConversationDetail{}, err
		}
	} else if reopened {
		_, err = tx.Exec(ctx, `
			UPDATE public.conversations
			SET `+archiveColumn+` = false,
				subject = CASE WHEN $2 = '' THEN subject ELSE $2 END,
				updated_at = GREATEST(
					clock_timestamp() AT TIME ZONE 'Asia/Shanghai',
					updated_at + interval '1 microsecond'
				)
			WHERE id = $1`, convID, subject)
		if err != nil {
			return conversationapp.ConversationDetail{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return conversationapp.ConversationDetail{}, err
	}

	detail, found, err := r.GetConversation(ctx, convID, creatorID, 1, 50)
	if err != nil {
		return conversationapp.ConversationDetail{}, err
	}
	if !found {
		return conversationapp.ConversationDetail{}, conversationapp.ErrNotFound
	}
	return detail, nil
}

// SendMessage adds a message, restores visibility, and updates last_message_at.
func (r ConversationRepository) SendMessage(ctx context.Context, conversationID string, senderID string, senderRole string, text string, attachments []messageattachment.Attachment) (conversationapp.Message, error) {
	msgID, err := newUUID()
	if err != nil {
		return conversationapp.Message{}, err
	}
	attachmentsJSON, err := messageattachment.Encode(attachments)
	if err != nil {
		return conversationapp.Message{}, err
	}

	tx, err := r.beginTx(ctx)
	if err != nil {
		return conversationapp.Message{}, err
	}
	defer tx.Rollback(ctx)

	var lockedSenderID string
	err = tx.QueryRow(ctx, `
		SELECT u.id
		FROM public.users u
		WHERE u.id = $1
		  AND u.is_active = true
		  AND (
			($2 = 'student' AND u.role::text = 'STUDENT')
			OR ($2 = 'teacher' AND u.role::text = 'TEACHER')
		  )
		FOR UPDATE`, senderID, senderRole).Scan(&lockedSenderID)
	if err == pgx.ErrNoRows {
		return conversationapp.Message{}, conversationapp.ErrNotFound
	}
	if err != nil {
		return conversationapp.Message{}, err
	}

	var parentLastMessageAt time.Time
	var recipientID string
	err = tx.QueryRow(ctx, `
		SELECT c.last_message_at,
			CASE WHEN $3 = 'student' THEN c.teacher_id ELSE c.student_id END
		FROM public.conversations c
		WHERE c.id = $1
		  AND (
			(c.student_id = $2 AND $3 = 'student')
			OR (c.teacher_id = $2 AND $3 = 'teacher')
		  )
		FOR UPDATE`, conversationID, senderID, senderRole).Scan(&parentLastMessageAt, &recipientID)
	if err == pgx.ErrNoRows {
		return conversationapp.Message{}, conversationapp.ErrNotFound
	}
	if err != nil {
		return conversationapp.Message{}, err
	}

	var messageAt time.Time
	err = tx.QueryRow(ctx, `
		INSERT INTO public.conversation_messages (id, conversation_id, sender_id, sender_role, text, attachments, created_at)
		VALUES (
			$1, $2, $3, $4, $5, $6,
			GREATEST(
				clock_timestamp() AT TIME ZONE 'Asia/Shanghai',
				$7::timestamp without time zone + interval '1 microsecond'
			)
		)
		RETURNING created_at`,
		msgID, conversationID, senderID, senderRole, text, string(attachmentsJSON), parentLastMessageAt,
	).Scan(&messageAt)
	if err != nil {
		return conversationapp.Message{}, err
	}

	_, err = tx.Exec(ctx, `
		UPDATE public.conversations
		SET student_archived = false,
			teacher_archived = false,
			last_message_at = $1,
			updated_at = $1
		WHERE id = $2`,
		messageAt, conversationID,
	)
	if err != nil {
		return conversationapp.Message{}, err
	}
	if err := r.wechatReminders.Enqueue(
		ctx,
		tx,
		wechatreminder.EventPrivateMessage,
		msgID,
		recipientID,
	); err != nil {
		return conversationapp.Message{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return conversationapp.Message{}, err
	}

	return conversationapp.Message{
		ID:          msgID,
		From:        senderRole,
		Text:        text,
		Time:        messageCenterWallTime(messageAt),
		Attachments: attachments,
	}, nil
}

// ArchiveConversation archives a conversation only for the requesting participant.
func (r ConversationRepository) ArchiveConversation(ctx context.Context, conversationID string, userID string, role user.Role) (bool, error) {
	archiveColumn := "student_archived"
	participantColumn := "student_id"
	if role == user.RoleTeacher {
		archiveColumn = "teacher_archived"
		participantColumn = "teacher_id"
	}
	tag, err := r.DB().Exec(ctx, `
		UPDATE public.conversations
		SET `+archiveColumn+` = true,
			updated_at = clock_timestamp() AT TIME ZONE 'Asia/Shanghai'
		WHERE id = $1 AND `+participantColumn+` = $2`,
		conversationID, userID,
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ListTeacherContacts returns the teachers that a student can message.
func (r ConversationRepository) ListTeacherContacts(ctx context.Context, studentID string) ([]conversationapp.Contact, error) {
	rows, err := r.DB().Query(ctx, `
		SELECT DISTINCT u.id, COALESCE(u.display_name, u.username), c.name
		FROM public.users u
		JOIN public.classes c ON c.teacher_id = u.id
		JOIN public.class_enrollments ce ON ce.class_id = c.id
		WHERE ce.student_id = $1 AND u.is_active = true
		ORDER BY u.id, c.name`,
		studentID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	contacts := make([]conversationapp.Contact, 0)
	for rows.Next() {
		var c conversationapp.Contact
		if err := rows.Scan(&c.ID, &c.DisplayName, &c.Scope); err != nil {
			return nil, err
		}
		contacts = append(contacts, c)
	}
	return contacts, rows.Err()
}

// ListStudentContacts returns students in the teacher's classes.
func (r ConversationRepository) ListStudentContacts(ctx context.Context, teacherID string) ([]conversationapp.Contact, error) {
	rows, err := r.DB().Query(ctx, `
		SELECT DISTINCT u.id, COALESCE(u.display_name, u.username), c.name
		FROM public.users u
		JOIN public.class_enrollments ce ON ce.student_id = u.id
		JOIN public.classes c ON c.id = ce.class_id
		WHERE c.teacher_id = $1 AND u.is_active = true
		ORDER BY u.id, c.name`, teacherID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	contacts := make([]conversationapp.Contact, 0)
	for rows.Next() {
		var c conversationapp.Contact
		if err := rows.Scan(&c.ID, &c.DisplayName, &c.Scope); err != nil {
			return nil, err
		}
		contacts = append(contacts, c)
	}
	return contacts, rows.Err()
}

// SearchContacts searches all users by ID or display name, filtered by role.
func (r ConversationRepository) SearchContacts(ctx context.Context, query string, role user.Role) ([]conversationapp.Contact, error) {
	targetRole := "TEACHER"
	if role == user.RoleTeacher {
		targetRole = "STUDENT"
	}
	rows, err := r.DB().Query(ctx, `
		SELECT u.id, COALESCE(u.display_name, u.username), '' AS scope
		FROM public.users u
		WHERE u.role::text = $1
		  AND u.is_active = true
		  AND (
			STRPOS(LOWER(u.id), LOWER($2)) > 0
			OR STRPOS(LOWER(COALESCE(u.display_name, '')), LOWER($2)) > 0
			OR STRPOS(LOWER(u.username), LOWER($2)) > 0
		  )
		ORDER BY COALESCE(u.display_name, u.username), u.id
		LIMIT 20`, targetRole, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	contacts := make([]conversationapp.Contact, 0)
	for rows.Next() {
		var c conversationapp.Contact
		if err := rows.Scan(&c.ID, &c.DisplayName, &c.Scope); err != nil {
			return nil, err
		}
		contacts = append(contacts, c)
	}
	return contacts, rows.Err()
}

func (r ConversationRepository) beginTx(ctx context.Context) (pgx.Tx, error) {
	if r.beginner == nil {
		return nil, conversationapp.ErrConflict
	}
	return r.beginner.BeginTx(ctx, pgx.TxOptions{})
}

func idxStr(n int) string {
	return strconv.Itoa(n)
}
