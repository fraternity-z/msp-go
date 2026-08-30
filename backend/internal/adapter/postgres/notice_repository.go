package postgres

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"mathstudy/backend/internal/application/messageattachment"
	noticeapp "mathstudy/backend/internal/application/notice"
	"mathstudy/backend/internal/domain/user"
)

// NoticeRepository persists notice data in PostgreSQL.
type NoticeRepository struct {
	Repository
	wechatReminders WechatReminderEnqueuer
}

// NewNoticeRepository creates a PostgreSQL-backed notice repository.
func NewNoticeRepository(db Querier, reminderEnqueuer WechatReminderEnqueuer) (NoticeRepository, error) {
	base, err := NewRepository(db)
	if err != nil {
		return NoticeRepository{}, err
	}
	return NoticeRepository{Repository: base, wechatReminders: reminderEnqueuer}, nil
}

// ListNotices returns paginated notices for a user.
func (r NoticeRepository) ListNotices(ctx context.Context, userID string, role user.Role, search string, status string, className string, page int, pageSize int) ([]any, int, error) {
	pgPage, err := NewPage((page-1)*pageSize, pageSize)
	if err != nil {
		return nil, 0, err
	}

	if role == user.RoleStudent {
		return r.listStudentNotices(ctx, userID, search, status, pgPage)
	}
	return r.listTeacherNotices(ctx, userID, search, status, className, pgPage)
}

func (r NoticeRepository) listStudentNotices(ctx context.Context, studentID string, search string, status string, page Page) ([]any, int, error) {
	where := " WHERE nr.student_id = $1"
	args := []any{studentID}
	idx := 2

	for _, term := range strings.Fields(search) {
		where += ` AND (
			STRPOS(LOWER(n.title), LOWER($` + idxStr(idx) + `)) > 0
			OR STRPOS(LOWER(n.body), LOWER($` + idxStr(idx) + `)) > 0
		)`
		args = append(args, term)
		idx++
	}
	switch status {
	case "待确认":
		where += ` AND nc.notice_id IS NULL`
	case "已确认":
		where += ` AND nc.notice_id IS NOT NULL`
	}

	from := `
		FROM public.notice_recipients nr
		JOIN public.notices n ON n.id = nr.notice_id
		LEFT JOIN public.notice_confirmations nc
		  ON nc.notice_id = nr.notice_id AND nc.student_id = nr.student_id`

	var total int
	if err := r.DB().QueryRow(ctx, `SELECT COUNT(*)`+from+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	countArgs := len(args)
	args = append(args, page.Limit, page.Offset)
	rows, err := r.DB().Query(ctx, `
		SELECT n.id, n.class_name, n.title, n.created_at,
			nc.notice_id IS NOT NULL AS confirmed`+from+where+`
		ORDER BY n.created_at DESC, n.id DESC
		LIMIT $`+idxStr(countArgs+1)+` OFFSET $`+idxStr(countArgs+2),
		args...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]any, 0)
	for rows.Next() {
		var item noticeapp.StudentNoticeListItem
		if err := rows.Scan(&item.ID, &item.ClassName, &item.Title, &item.PublishedAt, &item.Confirmed); err != nil {
			return nil, 0, err
		}
		item.PublishedAt = messageCenterWallTime(item.PublishedAt)
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (r NoticeRepository) listTeacherNotices(ctx context.Context, teacherID string, search string, status string, className string, page Page) ([]any, int, error) {
	where := " WHERE n.teacher_id = $1"
	args := []any{teacherID}
	idx := 2

	for _, term := range strings.Fields(search) {
		where += ` AND (
			STRPOS(LOWER(n.title), LOWER($` + idxStr(idx) + `)) > 0
			OR STRPOS(LOWER(n.body), LOWER($` + idxStr(idx) + `)) > 0
		)`
		args = append(args, term)
		idx++
	}
	if strings.TrimSpace(className) != "" {
		where += ` AND STRPOS(LOWER(n.class_name), LOWER($` + idxStr(idx) + `)) > 0`
		args = append(args, className)
	}
	switch status {
	case "有未确认":
		where += ` AND EXISTS (
			SELECT 1
			FROM public.notice_recipients nr
			WHERE nr.notice_id = n.id
			  AND NOT EXISTS (
				  SELECT 1
				  FROM public.notice_confirmations nc
				  WHERE nc.notice_id = nr.notice_id AND nc.student_id = nr.student_id
			  )
		)`
	case "全部确认":
		where += ` AND NOT EXISTS (
			SELECT 1
			FROM public.notice_recipients nr
			WHERE nr.notice_id = n.id
			  AND NOT EXISTS (
				  SELECT 1
				  FROM public.notice_confirmations nc
				  WHERE nc.notice_id = nr.notice_id AND nc.student_id = nr.student_id
			  )
		)`
	}

	var total int
	if err := r.DB().QueryRow(ctx, `
		SELECT COUNT(*)
		FROM public.notices n`+where,
		args...,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	countArgs := len(args)
	args = append(args, page.Limit, page.Offset)
	rows, err := r.DB().Query(ctx, `
		SELECT n.id, n.class_name, n.title, n.created_at,
			COALESCE(recipient_counts.confirmed_count, 0),
			COALESCE(recipient_counts.total_count, 0)
		FROM public.notices n
		LEFT JOIN LATERAL (
			SELECT COUNT(*) FILTER (WHERE nc.notice_id IS NOT NULL) AS confirmed_count,
				COUNT(*) AS total_count
			FROM public.notice_recipients nr
			LEFT JOIN public.notice_confirmations nc
			  ON nc.notice_id = nr.notice_id AND nc.student_id = nr.student_id
			WHERE nr.notice_id = n.id
		) recipient_counts ON true
		`+where+`
		ORDER BY n.created_at DESC, n.id DESC
		LIMIT $`+idxStr(countArgs+1)+` OFFSET $`+idxStr(countArgs+2),
		args...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]any, 0)
	for rows.Next() {
		var item noticeapp.TeacherNoticeListItem
		if err := rows.Scan(&item.ID, &item.ClassName, &item.Title, &item.PublishedAt,
			&item.ConfirmedCount, &item.TotalCount); err != nil {
			return nil, 0, err
		}
		item.PublishedAt = messageCenterWallTime(item.PublishedAt)
		items = append(items, item)
	}
	return items, total, rows.Err()
}

// GetNotice returns a single notice.
func (r NoticeRepository) GetNotice(ctx context.Context, noticeID string, userID string, role user.Role) (any, bool, error) {
	if role == user.RoleStudent {
		return r.getStudentNotice(ctx, noticeID, userID)
	}
	return r.getTeacherNotice(ctx, noticeID, userID)
}

func (r NoticeRepository) getStudentNotice(ctx context.Context, noticeID string, studentID string) (any, bool, error) {
	var item noticeapp.StudentNoticeItem
	var attachmentsJSON []byte
	err := r.DB().QueryRow(ctx, `
		SELECT n.id, n.class_name, n.title, n.created_at,
			nc.notice_id IS NOT NULL AS confirmed,
			n.body, n.attachments
		FROM public.notice_recipients nr
		JOIN public.notices n ON n.id = nr.notice_id
		LEFT JOIN public.notice_confirmations nc
		  ON nc.notice_id = nr.notice_id AND nc.student_id = nr.student_id
		WHERE nr.notice_id = $1 AND nr.student_id = $2`,
		noticeID, studentID,
	).Scan(&item.ID, &item.ClassName, &item.Title, &item.PublishedAt, &item.Confirmed, &item.Body, &attachmentsJSON)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}
	item.PublishedAt = messageCenterWallTime(item.PublishedAt)
	item.Attachments, err = messageattachment.Decode(attachmentsJSON)
	if err != nil {
		return nil, false, err
	}
	return item, true, nil
}

func (r NoticeRepository) getTeacherNotice(ctx context.Context, noticeID string, teacherID string) (any, bool, error) {
	var item noticeapp.TeacherNoticeItem
	var attachmentsJSON []byte
	err := r.DB().QueryRow(ctx, `
			SELECT n.id, n.class_name, n.title, n.body, n.created_at, n.attachments,
				COALESCE(recipient_counts.confirmed_count, 0),
				COALESCE(recipient_counts.total_count, 0),
				COALESCE(recipient_counts.unconfirmed_students, ARRAY[]::character varying[])
		FROM public.notices n
			LEFT JOIN LATERAL (
				SELECT COUNT(*) FILTER (WHERE nc.notice_id IS NOT NULL) AS confirmed_count,
					COUNT(*) AS total_count,
					array_agg(nr.recipient_name ORDER BY nr.recipient_name, nr.student_id)
						FILTER (WHERE nc.notice_id IS NULL) AS unconfirmed_students
			FROM public.notice_recipients nr
			LEFT JOIN public.notice_confirmations nc
			  ON nc.notice_id = nr.notice_id AND nc.student_id = nr.student_id
			WHERE nr.notice_id = n.id
		) recipient_counts ON true
		WHERE n.id = $1 AND n.teacher_id = $2`,
		noticeID, teacherID,
	).Scan(&item.ID, &item.ClassName, &item.Title, &item.Body, &item.PublishedAt, &attachmentsJSON,
		&item.ConfirmedCount, &item.TotalCount, &item.UnconfirmedStudents)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}
	item.PublishedAt = messageCenterWallTime(item.PublishedAt)
	item.Attachments, err = messageattachment.Decode(attachmentsJSON)
	if err != nil {
		return nil, false, err
	}

	return item, true, nil
}

// CreateNotice publishes a notice and snapshots its active recipients atomically.
func (r NoticeRepository) CreateNotice(ctx context.Context, teacherID string, classID string, title string, body string, attachments []messageattachment.Attachment, now time.Time) (noticeapp.TeacherNoticeItem, error) {
	var created noticeapp.TeacherNoticeItem
	err := withRepositoryTx(ctx, "notice publication", r.Repository, func(base Repository) NoticeRepository {
		return NoticeRepository{Repository: base, wechatReminders: r.wechatReminders}
	}, func(txRepo NoticeRepository) error {
		item, err := txRepo.createNotice(ctx, teacherID, classID, title, body, attachments, now)
		if err != nil {
			return err
		}
		created = item
		return nil
	})
	return created, err
}

func (r NoticeRepository) createNotice(ctx context.Context, teacherID string, classID string, title string, body string, attachments []messageattachment.Attachment, now time.Time) (noticeapp.TeacherNoticeItem, error) {
	now = messageCenterInstant(now)
	attachmentsJSON, err := messageattachment.Encode(attachments)
	if err != nil {
		return noticeapp.TeacherNoticeItem{}, err
	}
	var teacherActive bool
	if err := r.DB().QueryRow(ctx, `
		SELECT true
		FROM public.users
		WHERE id = $1 AND role = 'TEACHER'::public.userrole AND is_active = true
		FOR UPDATE`, teacherID).Scan(&teacherActive); err != nil {
		if err == pgx.ErrNoRows {
			return noticeapp.TeacherNoticeItem{}, noticeapp.ErrForbidden
		}
		return noticeapp.TeacherNoticeItem{}, err
	}

	var className string
	if err := r.DB().QueryRow(ctx, `
			SELECT c.name
			FROM public.classes c
			WHERE c.id = $1 AND c.teacher_id = $2
			FOR UPDATE`,
		classID, teacherID,
	).Scan(&className); err != nil {
		if err == pgx.ErrNoRows {
			return noticeapp.TeacherNoticeItem{}, noticeapp.ErrForbidden
		}
		return noticeapp.TeacherNoticeItem{}, err
	}

	id, err := newUUID()
	if err != nil {
		return noticeapp.TeacherNoticeItem{}, err
	}
	if _, err := r.DB().Exec(ctx, `
		INSERT INTO public.notices (id, teacher_id, class_id, class_name, title, body, attachments, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		id, teacherID, classID, className, title, body, string(attachmentsJSON), now,
	); err != nil {
		return noticeapp.TeacherNoticeItem{}, err
	}
	if err := r.wechatReminders.EnqueueNoticeRecipients(ctx, r.DB(), id); err != nil {
		return noticeapp.TeacherNoticeItem{}, err
	}

	studentRows, err := r.DB().Query(ctx, `
		SELECT recipient_name
		FROM public.notice_recipients
		WHERE notice_id = $1
		ORDER BY recipient_name, student_id`, id)
	if err != nil {
		return noticeapp.TeacherNoticeItem{}, err
	}
	defer studentRows.Close()

	names := make([]string, 0)
	for studentRows.Next() {
		var name string
		if err := studentRows.Scan(&name); err != nil {
			return noticeapp.TeacherNoticeItem{}, err
		}
		names = append(names, name)
	}
	if err := studentRows.Err(); err != nil {
		return noticeapp.TeacherNoticeItem{}, err
	}

	return noticeapp.TeacherNoticeItem{
		ID:                  id,
		ClassName:           className,
		Title:               title,
		Body:                body,
		PublishedAt:         now,
		ConfirmedCount:      0,
		TotalCount:          len(names),
		UnconfirmedStudents: names,
		Attachments:         attachments,
	}, nil
}

// RemindUnconfirmed requeues reminder jobs for a teacher-owned notice's current unconfirmed recipients.
func (r NoticeRepository) RemindUnconfirmed(ctx context.Context, noticeID string, teacherID string) (noticeapp.ReminderResult, error) {
	if !r.wechatReminders.Enabled() {
		return noticeapp.ReminderResult{}, noticeapp.ErrReminderUnavailable
	}
	var result noticeapp.ReminderResult
	err := withRepositoryTx(ctx, "notice reminder", r.Repository, func(base Repository) NoticeRepository {
		return NoticeRepository{Repository: base, wechatReminders: r.wechatReminders}
	}, func(txRepo NoticeRepository) error {
		var owned bool
		if err := txRepo.DB().QueryRow(ctx, `
			SELECT true
			FROM public.notices
			WHERE id = $1 AND teacher_id = $2
			FOR UPDATE`, noticeID, teacherID).Scan(&owned); err != nil {
			if err == pgx.ErrNoRows {
				return noticeapp.ErrNotFound
			}
			return err
		}

		rows, err := txRepo.DB().Query(ctx, `
			SELECT nr.recipient_name
			FROM public.notice_recipients nr
			WHERE nr.notice_id = $1
			  AND NOT EXISTS (
				SELECT 1
				FROM public.notice_confirmations nc
				WHERE nc.notice_id = nr.notice_id AND nc.student_id = nr.student_id
			  )
			ORDER BY nr.recipient_name, nr.student_id`, noticeID)
		if err != nil {
			return err
		}
		names := make([]string, 0)
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return err
			}
			names = append(names, name)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		queued, err := txRepo.wechatReminders.RequeueUnconfirmedNoticeRecipients(ctx, txRepo.DB(), noticeID)
		if err != nil {
			return err
		}
		result = noticeapp.ReminderResult{
			UnconfirmedStudents: names,
			Count:               len(names),
			QueuedCount:         queued,
		}
		return nil
	})
	return result, err
}

// ConfirmNotice marks a notice as confirmed by one of its snapshotted recipients.
func (r NoticeRepository) ConfirmNotice(ctx context.Context, noticeID string, studentID string) (bool, error) {
	id, err := newUUID()
	if err != nil {
		return false, err
	}

	var noticeExists bool
	var recipient bool
	err = r.DB().QueryRow(ctx, `
		WITH target_notice AS (
			SELECT 1
			FROM public.notices
			WHERE id = $1
		), recipient AS (
			SELECT 1
			FROM public.notice_recipients
			WHERE notice_id = $1 AND student_id = $2
		), inserted AS (
			INSERT INTO public.notice_confirmations (id, notice_id, student_id, confirmed_at)
				SELECT $3, $1, $2, clock_timestamp() AT TIME ZONE 'Asia/Shanghai'
			WHERE EXISTS (SELECT 1 FROM recipient)
			ON CONFLICT (notice_id, student_id) DO NOTHING
			RETURNING 1
		)
		SELECT EXISTS (SELECT 1 FROM target_notice),
			EXISTS (SELECT 1 FROM recipient)`,
		noticeID, studentID, id,
	).Scan(&noticeExists, &recipient)
	if err != nil {
		return false, err
	}
	if !noticeExists {
		return false, noticeapp.ErrNotFound
	}
	return recipient, nil
}
