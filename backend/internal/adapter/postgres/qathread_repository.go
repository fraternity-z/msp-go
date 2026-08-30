package postgres

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"mathstudy/backend/internal/application/messageattachment"
	qathreadapp "mathstudy/backend/internal/application/qathread"
	wechatreminder "mathstudy/backend/internal/application/wechatreminder"
	"mathstudy/backend/internal/domain/user"
)

// QAThreadRepository persists Q&A thread data in PostgreSQL.
type QAThreadRepository struct {
	Repository
	wechatReminders WechatReminderEnqueuer
}

// NewQAThreadRepository creates a PostgreSQL-backed Q&A thread repository.
func NewQAThreadRepository(db Querier, reminderEnqueuer WechatReminderEnqueuer) (QAThreadRepository, error) {
	base, err := NewRepository(db)
	if err != nil {
		return QAThreadRepository{}, err
	}
	return QAThreadRepository{Repository: base, wechatReminders: reminderEnqueuer}, nil
}

// ListThreads returns paginated threads for a user.
func (r QAThreadRepository) ListThreads(ctx context.Context, userID string, role user.Role, search string, status string, className string, teacherID string, page int, pageSize int) ([]any, int, error) {
	pgPage, err := NewPage((page-1)*pageSize, pageSize)
	if err != nil {
		return nil, 0, err
	}
	if role == user.RoleStudent {
		return r.listStudentThreads(ctx, userID, search, teacherID, pgPage)
	}
	return r.listTeacherThreads(ctx, userID, search, status, className, pgPage)
}

func (r QAThreadRepository) listStudentThreads(ctx context.Context, studentID string, search string, teacherID string, page Page) ([]any, int, error) {
	where := " WHERE qt.student_id = $1"
	args := []any{studentID}
	idx := 2
	for _, term := range strings.Fields(search) {
		where += ` AND (
			STRPOS(LOWER(qt.title), LOWER($` + idxStr(idx) + `)) > 0
			OR STRPOS(LOWER(qt.context), LOWER($` + idxStr(idx) + `)) > 0
			OR STRPOS(LOWER(qt.source), LOWER($` + idxStr(idx) + `)) > 0
			OR STRPOS(LOWER(COALESCE(qt.knowledge_point, '')), LOWER($` + idxStr(idx) + `)) > 0
			OR STRPOS(LOWER(COALESCE(qt.resource_name, '')), LOWER($` + idxStr(idx) + `)) > 0
			OR STRPOS(LOWER(COALESCE(u.display_name, u.username)), LOWER($` + idxStr(idx) + `)) > 0
			OR STRPOS(LOWER(COALESCE(cls.name, qt.class_name, '')), LOWER($` + idxStr(idx) + `)) > 0
		)`
		args = append(args, term)
		idx++
	}
	if strings.TrimSpace(teacherID) != "" {
		where += ` AND qt.teacher_id = $` + idxStr(idx)
		args = append(args, teacherID)
	}

	var total int
	if err := r.DB().QueryRow(ctx, `
		SELECT COUNT(*)
		FROM public.question_threads qt
		JOIN public.users u ON u.id = qt.teacher_id
		LEFT JOIN public.classes cls ON cls.id = qt.class_id
		`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	countArgs := len(args)
	args = append(args, page.Limit, page.Offset)

	rows, err := r.DB().Query(ctx, `
		SELECT qt.id, qt.title, qt.teacher_id,
			COALESCE(u.display_name, u.username),
			qt.source, LEFT(qt.context, 500), qt.status,
			COALESCE(qt.class_id, ''), COALESCE(cls.name, qt.class_name, ''),
			EXISTS (
				SELECT 1 FROM public.question_thread_messages qtm
				WHERE qtm.thread_id = qt.id
				  AND qtm.sender_role = 'teacher'
				  AND qtm.read_at IS NULL
			),
			qt.updated_at
		FROM public.question_threads qt
		JOIN public.users u ON u.id = qt.teacher_id
		LEFT JOIN public.classes cls ON cls.id = qt.class_id
		`+where+`
		ORDER BY qt.updated_at DESC, qt.id DESC
		LIMIT $`+idxStr(countArgs+1)+` OFFSET $`+idxStr(countArgs+2),
		args...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]any, 0)
	for rows.Next() {
		var item qathreadapp.StudentThreadItem
		if err := rows.Scan(&item.ID, &item.Title, &item.TeacherID, &item.TeacherName, &item.Source, &item.ContextPreview,
			&item.Status, &item.ClassID, &item.ClassName, &item.Unread, &item.LastUpdate); err != nil {
			return nil, 0, err
		}
		item.LastUpdate = messageCenterWallTime(item.LastUpdate)
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (r QAThreadRepository) listTeacherThreads(ctx context.Context, teacherID string, search string, status string, className string, page Page) ([]any, int, error) {
	where := " WHERE qt.teacher_id = $1"
	args := []any{teacherID}
	idx := 2
	for _, term := range strings.Fields(search) {
		where += ` AND (
			STRPOS(LOWER(qt.title), LOWER($` + idxStr(idx) + `)) > 0
			OR STRPOS(LOWER(qt.context), LOWER($` + idxStr(idx) + `)) > 0
			OR STRPOS(LOWER(COALESCE(u.display_name, u.username)), LOWER($` + idxStr(idx) + `)) > 0
			OR STRPOS(LOWER(COALESCE(cls.name, qt.class_name, '')), LOWER($` + idxStr(idx) + `)) > 0
			OR STRPOS(LOWER(qt.source), LOWER($` + idxStr(idx) + `)) > 0
			OR STRPOS(LOWER(COALESCE(qt.knowledge_point, '')), LOWER($` + idxStr(idx) + `)) > 0
			OR STRPOS(LOWER(COALESCE(qt.resource_name, '')), LOWER($` + idxStr(idx) + `)) > 0
		)`
		args = append(args, term)
		idx++
	}
	if strings.TrimSpace(status) != "" && status != "全部" {
		where += ` AND qt.status = $` + idxStr(idx)
		args = append(args, status)
		idx++
	}
	if strings.TrimSpace(className) != "" {
		where += ` AND STRPOS(LOWER(COALESCE(cls.name, qt.class_name, '')), LOWER($` + idxStr(idx) + `)) > 0`
		args = append(args, className)
	}

	var total int
	if err := r.DB().QueryRow(ctx, `
		SELECT COUNT(*)
		FROM public.question_threads qt
		JOIN public.users u ON u.id = qt.student_id
		LEFT JOIN public.classes cls ON cls.id = qt.class_id
		`+where, args...,
	).Scan(&total); err != nil {
		return nil, 0, err
	}
	countArgs := len(args)
	args = append(args, page.Limit, page.Offset)

	rows, err := r.DB().Query(ctx, `
		SELECT qt.id,
			COALESCE(u.display_name, u.username),
			COALESCE(qt.class_id, ''),
			COALESCE(cls.name, qt.class_name, ''),
			qt.title, qt.source,
			COALESCE(qt.knowledge_point, ''),
			COALESCE(qt.resource_name, ''),
			qt.status, LEFT(qt.context, 500), qt.updated_at
		FROM public.question_threads qt
		JOIN public.users u ON u.id = qt.student_id
		LEFT JOIN public.classes cls ON cls.id = qt.class_id
		`+where+`
		ORDER BY qt.updated_at DESC, qt.id DESC
		LIMIT $`+idxStr(countArgs+1)+` OFFSET $`+idxStr(countArgs+2),
		args...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]any, 0)
	for rows.Next() {
		var item qathreadapp.TeacherThreadItem
		var resourceName, knowledgePoint pgtype.Text
		if err := rows.Scan(&item.ID, &item.StudentName, &item.ClassID, &item.ClassName, &item.Title, &item.Source,
			&knowledgePoint, &resourceName, &item.Status, &item.ContextPreview, &item.LastUpdate); err != nil {
			return nil, 0, err
		}
		item.LastUpdate = messageCenterWallTime(item.LastUpdate)
		if knowledgePoint.Valid {
			item.KnowledgePoint = knowledgePoint.String
		}
		if resourceName.Valid {
			item.ResourceName = resourceName.String
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

// GetThread returns a thread with full message history.
func (r QAThreadRepository) GetThread(ctx context.Context, threadID string, userID string, role user.Role, page, pageSize int) (any, bool, error) {
	if role == user.RoleStudent {
		return r.getStudentThread(ctx, threadID, userID, page, pageSize)
	}
	return r.getTeacherThread(ctx, threadID, userID, page, pageSize)
}

// AcknowledgeThreadRead marks messages from the other participant no newer than a delivered message cutoff.
func (r QAThreadRepository) AcknowledgeThreadRead(ctx context.Context, threadID string, userID string, role user.Role, throughMessageID string) (bool, error) {
	var valid bool
	var updated int
	err := r.DB().QueryRow(ctx, `
		WITH authorized_cutoff AS (
			SELECT qtm.created_at, qtm.id,
				CASE
					WHEN $4::text = 'student' THEN 'teacher'
					WHEN $4::text = 'teacher' THEN 'student'
				END AS sender_role
			FROM public.question_thread_messages qtm
			JOIN public.question_threads qt ON qt.id = qtm.thread_id
			WHERE qtm.thread_id = $1
			  AND qtm.id = $3
			  AND (
				($4::text = 'student' AND qt.student_id = $2)
				OR ($4::text = 'teacher' AND qt.teacher_id = $2)
			  )
		), updated AS (
			UPDATE public.question_thread_messages target
			SET read_at = clock_timestamp() AT TIME ZONE 'Asia/Shanghai'
			FROM authorized_cutoff cutoff
			WHERE target.thread_id = $1
			  AND target.sender_role = cutoff.sender_role
			  AND target.read_at IS NULL
			  AND (target.created_at, target.id) <= (cutoff.created_at, cutoff.id)
			RETURNING 1
		)
		SELECT EXISTS (SELECT 1 FROM authorized_cutoff), COUNT(*) FROM updated`,
		threadID, userID, throughMessageID, string(role),
	).Scan(&valid, &updated)
	return valid, err
}

func (r QAThreadRepository) getStudentThread(ctx context.Context, threadID string, studentID string, page, pageSize int) (any, bool, error) {
	var detail qathreadapp.ThreadDetail
	err := r.DB().QueryRow(ctx, `
		SELECT qt.id, qt.title, qt.teacher_id,
			COALESCE(u.display_name, u.username),
			COALESCE(qt.class_id, ''), COALESCE(cls.name, qt.class_name, ''),
			qt.source, qt.context, qt.status
		FROM public.question_threads qt
		JOIN public.users u ON u.id = qt.teacher_id
		LEFT JOIN public.classes cls ON cls.id = qt.class_id
		WHERE qt.id = $1 AND qt.student_id = $2`,
		threadID, studentID,
	).Scan(&detail.ID, &detail.Title, &detail.TeacherID, &detail.TeacherName, &detail.ClassID, &detail.ClassName,
		&detail.Source, &detail.Context, &detail.Status)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}
	msgs, total, readThroughMessageID, err := r.loadThreadMessages(ctx, threadID, page, pageSize)
	if err != nil {
		return nil, false, err
	}
	detail.Messages = msgs
	detail.MessagesTotal, detail.MessagesPage, detail.MessagesSize = total, page, pageSize
	detail.ReadThroughMessageID = readThroughMessageID
	return detail, true, nil
}

func (r QAThreadRepository) getTeacherThread(ctx context.Context, threadID string, teacherID string, page, pageSize int) (any, bool, error) {
	var detail qathreadapp.ThreadDetail
	var knowledgePoint, resourceName pgtype.Text
	err := r.DB().QueryRow(ctx, `
		SELECT qt.id,
			COALESCE(u.display_name, u.username),
			COALESCE(qt.class_id, ''), COALESCE(cls.name, qt.class_name, ''),
			qt.title, qt.source,
			COALESCE(qt.knowledge_point, ''),
			COALESCE(qt.resource_name, ''),
			qt.status, qt.context
		FROM public.question_threads qt
		JOIN public.users u ON u.id = qt.student_id
		LEFT JOIN public.classes cls ON cls.id = qt.class_id
		WHERE qt.id = $1 AND qt.teacher_id = $2`,
		threadID, teacherID,
	).Scan(&detail.ID, &detail.StudentName, &detail.ClassID, &detail.ClassName, &detail.Title, &detail.Source,
		&knowledgePoint, &resourceName, &detail.Status, &detail.Context)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}
	if knowledgePoint.Valid {
		detail.KnowledgePoint = knowledgePoint.String
	}
	if resourceName.Valid {
		detail.ResourceName = resourceName.String
	}
	msgs, total, readThroughMessageID, err := r.loadThreadMessages(ctx, threadID, page, pageSize)
	if err != nil {
		return nil, false, err
	}
	detail.Messages = msgs
	detail.MessagesTotal, detail.MessagesPage, detail.MessagesSize = total, page, pageSize
	detail.ReadThroughMessageID = readThroughMessageID
	return detail, true, nil
}

func (r QAThreadRepository) loadThreadMessages(ctx context.Context, threadID string, page, pageSize int) ([]qathreadapp.Message, int, string, error) {
	if page < 1 {
		page = 1
	}
	pgPage, err := NewPage((page-1)*pageSize, pageSize)
	if err != nil {
		return nil, 0, "", err
	}
	var total int
	if err := r.DB().QueryRow(ctx, `SELECT COUNT(*) FROM public.question_thread_messages WHERE thread_id = $1`, threadID).Scan(&total); err != nil {
		return nil, 0, "", err
	}
	rows, err := r.DB().Query(ctx, `
		SELECT id, sender_role, text, created_at, attachments
		FROM public.question_thread_messages
		WHERE thread_id = $1
		ORDER BY created_at DESC, id DESC LIMIT $2 OFFSET $3`, threadID, pgPage.Limit, pgPage.Offset)
	if err != nil {
		return nil, 0, "", err
	}
	defer rows.Close()
	msgs := make([]qathreadapp.Message, 0)
	for rows.Next() {
		var m qathreadapp.Message
		var attachmentsJSON []byte
		if err := rows.Scan(&m.ID, &m.From, &m.Text, &m.Time, &attachmentsJSON); err != nil {
			return nil, 0, "", err
		}
		m.Attachments, err = messageattachment.Decode(attachmentsJSON)
		if err != nil {
			return nil, 0, "", err
		}
		m.Time = messageCenterWallTime(m.Time)
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, "", err
	}
	for left, right := 0, len(msgs)-1; left < right; left, right = left+1, right-1 {
		msgs[left], msgs[right] = msgs[right], msgs[left]
	}
	readThroughMessageID := ""
	if len(msgs) > 0 {
		readThroughMessageID = msgs[len(msgs)-1].ID
	}
	return msgs, total, readThroughMessageID, nil
}

// extractQuestionPart returns the student's own question (before the --- separator).
func extractQuestionPart(content string) string {
	parts := strings.SplitN(content, "\n---\n", 2)
	if len(parts) == 2 {
		q := strings.TrimSpace(parts[0])
		if q != "" && !strings.HasPrefix(q, "【原题】") {
			return q
		}
	}
	return ""
}

// extractTitle returns a short title, preferring the 【原题】 line for imports.
func extractTitle(content string, maxLen int) string {
	// First pass: look for a line that starts with 【原题】
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "【原题】") {
			cleaned := strings.TrimPrefix(trimmed, "【原题】")
			cleaned = strings.TrimSpace(cleaned)
			if cleaned != "" {
				runes := []rune(cleaned)
				if len(runes) > maxLen {
					return string(runes[:maxLen]) + "..."
				}
				return cleaned
			}
		}
	}
	// Second pass: first non-empty line
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		runes := []rune(trimmed)
		if len(runes) > maxLen {
			return string(runes[:maxLen]) + "..."
		}
		return trimmed
	}
	return ""
}

// CreateThread creates a new question thread with the first message.
func (r QAThreadRepository) CreateThread(ctx context.Context, studentID string, teacherID string, content string, source string, attachments []messageattachment.Attachment) (qathreadapp.ThreadDetail, error) {
	threadID, err := newUUID()
	if err != nil {
		return qathreadapp.ThreadDetail{}, err
	}
	attachmentsJSON, err := messageattachment.Encode(attachments)
	if err != nil {
		return qathreadapp.ThreadDetail{}, err
	}
	title := extractTitle(content, 30)
	if title == "" && len(attachments) > 0 {
		title = "附件问题"
	}
	threadContext := content
	firstMsg := content
	if strings.Contains(content, "【原题】") {
		questionPart := extractQuestionPart(content)
		if questionPart != "" {
			firstMsg = questionPart
			// Keep only the mistake details in context (strip the question prefix)
			parts := strings.SplitN(content, "\n---\n", 2)
			if len(parts) == 2 {
				threadContext = strings.TrimSpace(parts[1])
			}
		} else {
			firstMsg = "从" + source + "导入了一道题目，请老师帮忙分析"
		}
	}

	tx, err := r.beginTx(ctx)
	if err != nil {
		return qathreadapp.ThreadDetail{}, err
	}
	defer tx.Rollback(ctx)

	var lockedUserCount int
	var teacherName string
	err = tx.QueryRow(ctx, `
		WITH locked_users AS MATERIALIZED (
			SELECT u.id, COALESCE(u.display_name, u.username) AS display_name
			FROM public.users u
			WHERE u.is_active = true
			  AND (
				(u.id = $1 AND u.role::text = 'STUDENT')
				OR (u.id = $2 AND u.role::text = 'TEACHER')
			  )
			ORDER BY u.id
			FOR UPDATE
		)
		SELECT count(*),
			COALESCE(max(display_name) FILTER (WHERE id = $2), '')
		FROM locked_users`, studentID, teacherID).Scan(&lockedUserCount, &teacherName)
	if err != nil {
		return qathreadapp.ThreadDetail{}, err
	}
	if lockedUserCount != 2 {
		return qathreadapp.ThreadDetail{}, qathreadapp.ErrForbidden
	}

	// Match DisbandClass's enrollment -> class lock order.
	var classID string
	err = tx.QueryRow(ctx, `
		SELECT ce.class_id
		FROM public.class_enrollments ce
		JOIN public.classes c ON c.id = ce.class_id
		WHERE c.teacher_id = $1
		  AND ce.student_id = $2
		FOR UPDATE OF ce`, teacherID, studentID).Scan(&classID)
	if err == pgx.ErrNoRows {
		return qathreadapp.ThreadDetail{}, qathreadapp.ErrForbidden
	}
	if err != nil {
		return qathreadapp.ThreadDetail{}, err
	}

	var className string
	err = tx.QueryRow(ctx, `
		SELECT c.name
		FROM public.classes c
		WHERE c.id = $1
		  AND c.teacher_id = $2
		FOR UPDATE`, classID, teacherID).Scan(&className)
	if err == pgx.ErrNoRows {
		return qathreadapp.ThreadDetail{}, qathreadapp.ErrForbidden
	}
	if err != nil {
		return qathreadapp.ThreadDetail{}, err
	}

	var parentUpdatedAt time.Time
	err = tx.QueryRow(ctx, `
		WITH stamped AS (
			SELECT clock_timestamp() AT TIME ZONE 'Asia/Shanghai' AS created_at
		)
		INSERT INTO public.question_threads (
			id, student_id, teacher_id, class_id, class_name,
			title, source, context, status, created_at, updated_at
		)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8, '待回复', stamped.created_at, stamped.created_at
		FROM stamped
		RETURNING updated_at`,
		threadID, studentID, teacherID, classID, className, title, source, threadContext,
	).Scan(&parentUpdatedAt)
	if err != nil {
		return qathreadapp.ThreadDetail{}, err
	}

	msgID, err := newUUID()
	if err != nil {
		return qathreadapp.ThreadDetail{}, err
	}
	var messageAt time.Time
	err = tx.QueryRow(ctx, `
		INSERT INTO public.question_thread_messages (id, thread_id, sender_id, sender_role, text, attachments, created_at)
		VALUES (
			$1, $2, $3, 'student', $4, $5,
			GREATEST(
				clock_timestamp() AT TIME ZONE 'Asia/Shanghai',
				$6::timestamp without time zone + interval '1 microsecond'
			)
		)
		RETURNING created_at`,
		msgID, threadID, studentID, firstMsg, string(attachmentsJSON), parentUpdatedAt,
	).Scan(&messageAt)
	if err != nil {
		return qathreadapp.ThreadDetail{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE public.question_threads
		SET updated_at = $2
		WHERE id = $1`, threadID, messageAt); err != nil {
		return qathreadapp.ThreadDetail{}, err
	}
	if err := r.wechatReminders.Enqueue(
		ctx,
		tx,
		wechatreminder.EventQAMessage,
		msgID,
		teacherID,
	); err != nil {
		return qathreadapp.ThreadDetail{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return qathreadapp.ThreadDetail{}, err
	}

	return qathreadapp.ThreadDetail{
		ID:                   threadID,
		TeacherID:            teacherID,
		TeacherName:          teacherName,
		ClassID:              classID,
		ClassName:            className,
		Title:                title,
		Source:               source,
		Context:              threadContext,
		Status:               "待回复",
		Messages:             []qathreadapp.Message{{ID: msgID, From: "student", Text: firstMsg, Time: messageCenterWallTime(messageAt), Attachments: attachments}},
		MessagesTotal:        1,
		MessagesPage:         1,
		MessagesSize:         50,
		ReadThroughMessageID: msgID,
	}, nil
}

// CreateThreadMessage adds a message to a thread and updates status.
func (r QAThreadRepository) CreateThreadMessage(ctx context.Context, threadID string, senderID string, senderRole string, text string, attachments []messageattachment.Attachment) (qathreadapp.Message, error) {
	msgID, err := newUUID()
	if err != nil {
		return qathreadapp.Message{}, err
	}
	attachmentsJSON, err := messageattachment.Encode(attachments)
	if err != nil {
		return qathreadapp.Message{}, err
	}

	tx, err := r.beginTx(ctx)
	if err != nil {
		return qathreadapp.Message{}, err
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
		return qathreadapp.Message{}, qathreadapp.ErrNotFound
	}
	if err != nil {
		return qathreadapp.Message{}, err
	}

	var parentUpdatedAt time.Time
	var recipientID string
	err = tx.QueryRow(ctx, `
		SELECT qt.updated_at,
			CASE WHEN $3 = 'student' THEN qt.teacher_id ELSE qt.student_id END
		FROM public.question_threads qt
		WHERE qt.id = $1
		  AND (
			(qt.student_id = $2 AND $3 = 'student')
			OR (qt.teacher_id = $2 AND $3 = 'teacher')
		  )
		FOR UPDATE`, threadID, senderID, senderRole).Scan(&parentUpdatedAt, &recipientID)
	if err == pgx.ErrNoRows {
		return qathreadapp.Message{}, qathreadapp.ErrNotFound
	}
	if err != nil {
		return qathreadapp.Message{}, err
	}

	var messageAt time.Time
	err = tx.QueryRow(ctx, `
		INSERT INTO public.question_thread_messages (id, thread_id, sender_id, sender_role, text, attachments, created_at)
		VALUES (
			$1, $2, $3, $4, $5, $6,
			GREATEST(
				clock_timestamp() AT TIME ZONE 'Asia/Shanghai',
				$7::timestamp without time zone + interval '1 microsecond'
			)
		)
		RETURNING created_at`,
		msgID, threadID, senderID, senderRole, text, string(attachmentsJSON), parentUpdatedAt,
	).Scan(&messageAt)
	if err != nil {
		return qathreadapp.Message{}, err
	}

	// Update status: student follow-up -> 待回复, teacher reply -> 已回复.
	newStatus := "待回复"
	if senderRole == "teacher" {
		newStatus = "已回复"
	}
	_, err = tx.Exec(ctx, `
		UPDATE public.question_threads SET status = $1, updated_at = $2 WHERE id = $3`,
		newStatus, messageAt, threadID,
	)
	if err != nil {
		return qathreadapp.Message{}, err
	}
	if err := r.wechatReminders.Enqueue(
		ctx,
		tx,
		wechatreminder.EventQAMessage,
		msgID,
		recipientID,
	); err != nil {
		return qathreadapp.Message{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return qathreadapp.Message{}, err
	}

	return qathreadapp.Message{ID: msgID, From: senderRole, Text: text, Time: messageCenterWallTime(messageAt), Attachments: attachments}, nil
}

// UpdateThreadStatus updates a thread's status (teacher only).
func (r QAThreadRepository) UpdateThreadStatus(ctx context.Context, threadID string, teacherID string, status string) (bool, error) {
	tag, err := r.DB().Exec(ctx, `
		UPDATE public.question_threads
		SET status = $1,
			updated_at = GREATEST(
				clock_timestamp() AT TIME ZONE 'Asia/Shanghai',
				updated_at + interval '1 microsecond'
			)
		WHERE id = $2 AND teacher_id = $3`,
		status, threadID, teacherID,
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r QAThreadRepository) beginTx(ctx context.Context) (pgx.Tx, error) {
	if r.beginner == nil {
		return nil, qathreadapp.ErrNotFound
	}
	return r.beginner.BeginTx(ctx, pgx.TxOptions{})
}
