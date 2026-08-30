package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	forumapp "mathstudy/backend/internal/application/forum"
	"mathstudy/backend/internal/application/messageattachment"
	"mathstudy/backend/internal/domain/user"
)

// ForumRepository persists global forum content and interaction notifications.
type ForumRepository struct {
	Repository
}

// NewForumRepository creates a PostgreSQL-backed forum repository.
func NewForumRepository(db Querier) (ForumRepository, error) {
	base, err := NewRepository(db)
	if err != nil {
		return ForumRepository{}, err
	}
	return ForumRepository{Repository: base}, nil
}

func (r ForumRepository) ListBoards(ctx context.Context) ([]forumapp.Board, error) {
	rows, err := r.DB().Query(ctx, `
		SELECT id, slug, name, description, sort_order
		FROM public.forum_boards
		WHERE is_active = true
		ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]forumapp.Board, 0)
	for rows.Next() {
		var item forumapp.Board
		if err := rows.Scan(&item.ID, &item.Slug, &item.Name, &item.Description, &item.SortOrder); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r ForumRepository) ListPosts(ctx context.Context, viewerID string, role user.Role, filter forumapp.ListPostsFilter) ([]forumapp.Post, int, error) {
	where := ` WHERE $1::text IS NOT NULL`
	// A soft-hidden post is retained for moderation/audit, but must not leak
	// into public or default administrator lists. Administrators can request
	// `all` or an explicit status when reviewing retained content.
	if role != user.RoleAdmin || filter.Status == "" || filter.Status == "visible" {
		where += ` AND p.status IN ('open', 'resolved')`
	}
	args := []any{viewerID}
	idx := 2
	if filter.BoardSlug != "" {
		where += ` AND b.slug = $` + idxStr(idx)
		args = append(args, filter.BoardSlug)
		idx++
	}
	if filter.Type != "" {
		where += ` AND p.post_type = $` + idxStr(idx)
		args = append(args, string(filter.Type))
		idx++
	}
	if filter.Status != "" && filter.Status != "all" && filter.Status != "visible" {
		where += ` AND p.status = $` + idxStr(idx)
		args = append(args, filter.Status)
		idx++
	}
	for _, term := range strings.Fields(filter.Search) {
		where += ` AND (
			STRPOS(LOWER(p.title), LOWER($` + idxStr(idx) + `)) > 0
			OR STRPOS(LOWER(p.content), LOWER($` + idxStr(idx) + `)) > 0
			OR STRPOS(LOWER(COALESCE(kn.name, '')), LOWER($` + idxStr(idx) + `)) > 0
			OR EXISTS (SELECT 1 FROM jsonb_array_elements_text(p.tags) tag WHERE STRPOS(LOWER(tag), LOWER($` + idxStr(idx) + `)) > 0)
		)`
		args = append(args, term)
		idx++
	}
	switch filter.Scope {
	case "mine":
		where += ` AND p.author_id = $1`
	case "replied":
		where += ` AND EXISTS (SELECT 1 FROM public.forum_replies mine WHERE mine.post_id = p.id AND mine.author_id = $1 AND mine.status = 'active')`
	case "favorites":
		where += ` AND EXISTS (SELECT 1 FROM public.forum_post_favorites mine WHERE mine.post_id = p.id AND mine.user_id = $1)`
	}
	// The featured view is a real scoped filter: it only contains selections
	// that are still valid for the viewing teacher or student.
	if filter.Sort == "featured" {
		where += ` AND ` + forumFeaturedForViewerSQL
	}

	from := `
		FROM public.forum_posts p
		JOIN public.forum_boards b ON b.id = p.board_id
		JOIN public.users author ON author.id = p.author_id
		LEFT JOIN public.knowledge_nodes kn ON kn.id = p.knowledge_node_id`
	var total int
	if err := r.DB().QueryRow(ctx, `SELECT COUNT(*)`+from+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	order := `is_featured_for_viewer DESC, p.updated_at DESC, p.id DESC`
	switch filter.Sort {
	case "hot":
		order = `is_featured_for_viewer DESC,
			(p.view_count +
			(SELECT COUNT(*) * 4 FROM public.forum_replies fr WHERE fr.post_id = p.id AND fr.status = 'active') +
			(SELECT COUNT(*) * 2 FROM public.forum_post_likes fl WHERE fl.post_id = p.id)) DESC,
				p.updated_at DESC, p.id DESC`
	case "featured":
		order = `p.featured_at DESC NULLS LAST, p.updated_at DESC, p.id DESC`
	}
	args = append(args, filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := r.DB().Query(ctx, forumPostSelect+from+where+`
		ORDER BY `+order+`
		LIMIT $`+idxStr(idx)+` OFFSET $`+idxStr(idx+1), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]forumapp.Post, 0)
	for rows.Next() {
		item, err := scanForumPost(rows, viewerID, role)
		if err != nil {
			return nil, 0, err
		}
		item.Content = ""
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (r ForumRepository) GetPost(ctx context.Context, postID, viewerID string, role user.Role, incrementView bool) (forumapp.PostDetail, bool, error) {
	isAdmin := role == user.RoleAdmin
	if incrementView && !isAdmin {
		if _, err := r.DB().Exec(ctx, `
			UPDATE public.forum_posts
			SET view_count = view_count + 1
			WHERE id = $1 AND status IN ('open', 'resolved')`, postID); err != nil {
			return forumapp.PostDetail{}, false, err
		}
	}
	row := r.DB().QueryRow(ctx, forumPostSelect+`
		FROM public.forum_posts p
		JOIN public.forum_boards b ON b.id = p.board_id
		JOIN public.users author ON author.id = p.author_id
		LEFT JOIN public.knowledge_nodes kn ON kn.id = p.knowledge_node_id
		WHERE p.id = $2 AND (p.status IN ('open', 'resolved') OR $3::boolean)`, viewerID, postID, isAdmin)
	post, err := scanForumPost(row, viewerID, role)
	if errors.Is(err, pgx.ErrNoRows) {
		return forumapp.PostDetail{}, false, nil
	}
	if err != nil {
		return forumapp.PostDetail{}, false, err
	}
	replies, err := r.listReplies(ctx, postID, viewerID, role, post.AcceptedReplyID)
	if err != nil {
		return forumapp.PostDetail{}, false, err
	}
	return forumapp.PostDetail{Post: post, Replies: replies}, true, nil
}

func (r ForumRepository) CreatePost(ctx context.Context, postID, authorID string, role user.Role, now time.Time, input forumapp.CreatePostInput) (forumapp.PostDetail, error) {
	input.BoardID = strings.TrimSpace(input.BoardID)
	input.BoardSlug = strings.TrimSpace(input.BoardSlug)
	input.Type = forumapp.PostType(strings.TrimSpace(string(input.Type)))
	if input.BoardID == "" && input.BoardSlug == "" {
		input.BoardSlug = forumapp.DefaultBoardSlug
	}
	if input.Type == "" {
		input.Type = forumapp.DefaultPostType
	}
	attachments, err := messageattachment.Encode(input.Attachments)
	if err != nil {
		return forumapp.PostDetail{}, err
	}
	tags, err := json.Marshal(input.Tags)
	if err != nil {
		return forumapp.PostDetail{}, err
	}
	var boardID string
	if input.BoardID != "" {
		err = r.DB().QueryRow(ctx, `SELECT id FROM public.forum_boards WHERE id = $1 AND is_active = true`, input.BoardID).Scan(&boardID)
	} else {
		err = r.DB().QueryRow(ctx, `SELECT id FROM public.forum_boards WHERE slug = $1 AND is_active = true`, input.BoardSlug).Scan(&boardID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return forumapp.PostDetail{}, forumapp.ErrInvalidInput
	}
	if err != nil {
		return forumapp.PostDetail{}, err
	}
	var knowledgeNodeID any
	if input.KnowledgeNodeID != "" {
		exists, err := r.Exists(ctx, `SELECT EXISTS (SELECT 1 FROM public.knowledge_nodes WHERE id = $1)`, input.KnowledgeNodeID)
		if err != nil {
			return forumapp.PostDetail{}, err
		}
		if !exists {
			return forumapp.PostDetail{}, forumapp.ErrInvalidInput
		}
		knowledgeNodeID = input.KnowledgeNodeID
	}
	_, err = r.DB().Exec(ctx, `
		INSERT INTO public.forum_posts (
			id, board_id, author_id, post_type, title, content, attachments, tags,
			knowledge_node_id, status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb, $9, 'open', $10, $10)`,
		postID, boardID, authorID, string(input.Type), input.Title, input.Content, attachments, tags, knowledgeNodeID, now)
	if err != nil {
		return forumapp.PostDetail{}, err
	}
	item, _, err := r.GetPost(ctx, postID, authorID, role, false)
	return item, err
}

func (r ForumRepository) UpdatePost(ctx context.Context, postID, actorID string, role user.Role, now time.Time, input forumapp.UpdatePostInput) (forumapp.PostDetail, bool, bool, error) {
	var result forumapp.PostDetail
	found := false
	allowed := false
	err := withRepositoryTx(ctx, "forum post update", r.Repository, func(base Repository) ForumRepository {
		return ForumRepository{Repository: base}
	}, func(current ForumRepository) error {
		var authorID string
		if err := current.DB().QueryRow(ctx, `
			SELECT author_id FROM public.forum_posts
			WHERE id = $1 AND status IN ('open', 'resolved') FOR UPDATE`, postID).Scan(&authorID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		found = true
		if authorID != actorID {
			return nil
		}
		allowed = true
		boardID, err := current.resolveUpdateBoard(ctx, input)
		if err != nil {
			return err
		}
		var postType, title, content any
		if input.Type != nil {
			postType = string(*input.Type)
		}
		if input.Title != nil {
			title = *input.Title
		}
		if input.Content != nil {
			content = *input.Content
		}
		var attachments, tags any
		if input.Attachments != nil {
			encoded, err := messageattachment.Encode(*input.Attachments)
			if err != nil {
				return err
			}
			attachments = encoded
		}
		if input.Tags != nil {
			encoded, err := json.Marshal(*input.Tags)
			if err != nil {
				return err
			}
			tags = encoded
		}
		knowledgeSet := input.KnowledgeNodeID != nil
		var knowledgeID any
		if knowledgeSet && *input.KnowledgeNodeID != "" {
			exists, err := current.Exists(ctx, `SELECT EXISTS (SELECT 1 FROM public.knowledge_nodes WHERE id = $1)`, *input.KnowledgeNodeID)
			if err != nil {
				return err
			}
			if !exists {
				return forumapp.ErrInvalidInput
			}
			knowledgeID = *input.KnowledgeNodeID
		}
		if _, err := current.DB().Exec(ctx, `
			UPDATE public.forum_posts SET
				board_id = COALESCE($2, board_id),
				post_type = COALESCE($3, post_type),
				title = COALESCE($4, title),
				content = COALESCE($5, content),
				attachments = COALESCE($6::jsonb, attachments),
				tags = COALESCE($7::jsonb, tags),
				knowledge_node_id = CASE WHEN $8 THEN $9 ELSE knowledge_node_id END,
				updated_at = $10
			WHERE id = $1`, postID, boardID, postType, title, content, attachments, tags, knowledgeSet, knowledgeID, now); err != nil {
			return err
		}
		item, ok, err := current.GetPost(ctx, postID, actorID, role, false)
		if err != nil {
			return err
		}
		if !ok {
			return forumapp.ErrNotFound
		}
		result = item
		return nil
	})
	return result, found, allowed, err
}

func (r ForumRepository) DeletePost(ctx context.Context, postID, actorID string, role user.Role, now time.Time) (bool, bool, error) {
	found := false
	allowed := false
	err := withRepositoryTx(ctx, "forum post delete", r.Repository, func(base Repository) ForumRepository {
		return ForumRepository{Repository: base}
	}, func(current ForumRepository) error {
		var authorID string
		if err := current.DB().QueryRow(ctx, `
			SELECT author_id FROM public.forum_posts
			WHERE id = $1
			  AND (status IN ('open', 'resolved') OR ($2::boolean AND status = 'hidden'))
			FOR UPDATE`, postID, role == user.RoleAdmin).Scan(&authorID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		found = true
		if authorID != actorID && role != user.RoleAdmin {
			return nil
		}
		allowed = true
		if _, err := current.DB().Exec(ctx, `
			UPDATE public.forum_posts
			SET status = 'hidden', deleted_at = $2, updated_at = $2,
				is_featured = false, featured_by = NULL, featured_at = NULL
			WHERE id = $1`, postID, now); err != nil {
			return err
		}
		if role == user.RoleAdmin {
			if _, err := current.DB().Exec(ctx, `
				UPDATE public.forum_reports report
				SET status = 'resolved', reviewed_by = $2, reviewed_at = $3
				WHERE report.status = 'pending'
				  AND (
					report.target_type = 'post' AND report.target_id = $1
					OR report.target_type = 'reply' AND EXISTS (
						SELECT 1 FROM public.forum_replies reply
						WHERE reply.id = report.target_id AND reply.post_id = $1
						)
				  )`, postID, actorID, now); err != nil {
				return err
			}
		}
		// Keep notification rows for audit/history, but make them inert once the
		// target is hidden. This avoids surfacing a hidden post in message-center
		// previews while preserving the recipient's read state.
		if _, err := current.DB().Exec(ctx, `
			UPDATE public.forum_notifications
			SET read_at = COALESCE(read_at, $2)
			WHERE post_id = $1 AND read_at IS NULL`, postID, now); err != nil {
			return err
		}
		return nil
	})
	return found, allowed, err
}

// RestorePost makes a hidden post public again, preserving whether it has an
// accepted reply. Already-visible posts are treated as an idempotent success;
// the second return value is false only for a non-restorable legacy state.
func (r ForumRepository) RestorePost(ctx context.Context, postID string, now time.Time) (bool, bool, error) {
	found := false
	restored := false
	err := withRepositoryTx(ctx, "forum post restore", r.Repository, func(base Repository) ForumRepository {
		return ForumRepository{Repository: base}
	}, func(current ForumRepository) error {
		var status string
		if err := current.DB().QueryRow(ctx, `
			SELECT status
			FROM public.forum_posts
			WHERE id = $1
			FOR UPDATE`, postID).Scan(&status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		found = true
		if status != "hidden" {
			restored = status == "open" || status == "resolved"
			return nil
		}
		if _, err := current.DB().Exec(ctx, `
			UPDATE public.forum_posts
			SET status = CASE WHEN accepted_reply_id IS NULL THEN 'open' ELSE 'resolved' END,
				deleted_at = NULL, updated_at = $2
			WHERE id = $1`, postID, now); err != nil {
			return err
		}
		restored = true
		return nil
	})
	return found, restored, err
}

// HardDeletePost permanently removes a post and all database-owned dependent
// rows. Reports use polymorphic IDs instead of foreign keys, so they must be
// removed explicitly before the post's cascading dependents disappear.
// Attachment URLs are embedded in JSON and can belong to any configured object
// storage backend. This repository deliberately does not delete those objects:
// storage deletion is an external side effect and there is no transactional
// object-delete contract here. A storage garbage-collector should reclaim
// unreferenced objects separately.
func (r ForumRepository) HardDeletePost(ctx context.Context, postID string) (bool, error) {
	found := false
	err := withRepositoryTx(ctx, "forum post hard delete", r.Repository, func(base Repository) ForumRepository {
		return ForumRepository{Repository: base}
	}, func(current ForumRepository) error {
		var lockedPostID string
		if err := current.DB().QueryRow(ctx, `
			SELECT id
			FROM public.forum_posts
			WHERE id = $1
			FOR UPDATE`, postID).Scan(&lockedPostID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		found = true
		if _, err := current.DB().Exec(ctx, `
			DELETE FROM public.forum_reports
			WHERE (target_type = 'post' AND target_id = $1)
			   OR (target_type = 'reply' AND target_id IN (
					SELECT id FROM public.forum_replies WHERE post_id = $1
				))`, postID); err != nil {
			return err
		}
		_, err := current.DB().Exec(ctx, `DELETE FROM public.forum_posts WHERE id = $1`, postID)
		return err
	})
	return found, err
}

func (r ForumRepository) CreateReply(ctx context.Context, replyID, postID, actorID string, role user.Role, now time.Time, input forumapp.CreateReplyInput) (forumapp.Reply, bool, error) {
	var result forumapp.Reply
	found := false
	err := withRepositoryTx(ctx, "forum reply create", r.Repository, func(base Repository) ForumRepository {
		return ForumRepository{Repository: base}
	}, func(current ForumRepository) error {
		var postAuthorID, postTitle string
		if err := current.DB().QueryRow(ctx, `
			SELECT author_id, title FROM public.forum_posts
			WHERE id = $1 AND status IN ('open', 'resolved') FOR UPDATE`, postID).Scan(&postAuthorID, &postTitle); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		found = true
		var parentAuthorID string
		if input.ParentReplyID != "" {
			if err := current.DB().QueryRow(ctx, `
				SELECT author_id FROM public.forum_replies
				WHERE id = $1 AND post_id = $2 AND status = 'active'`, input.ParentReplyID, postID).Scan(&parentAuthorID); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return forumapp.ErrInvalidInput
				}
				return err
			}
		}
		validMentionIDs := make(map[string]struct{}, len(input.MentionUserIDs))
		for _, mentionedID := range input.MentionUserIDs {
			var displayName string
			err := current.DB().QueryRow(ctx, `
					SELECT COALESCE(NULLIF(BTRIM(mentioned.display_name), ''), mentioned.username)
					FROM public.users mentioned
					WHERE mentioned.id = $2
					  AND mentioned.is_active = true
					  AND (
						mentioned.id = $3
						OR EXISTS (
							SELECT 1 FROM public.forum_replies candidate
							WHERE candidate.post_id = $1
							  AND candidate.author_id = mentioned.id
							  AND candidate.status = 'active'
						)
					  )`, postID, mentionedID, postAuthorID).Scan(&displayName)
			if errors.Is(err, pgx.ErrNoRows) {
				return forumapp.ErrInvalidInput
			}
			if err != nil {
				return err
			}
			if !strings.Contains(input.Content, "@"+displayName) {
				return forumapp.ErrInvalidInput
			}
			validMentionIDs[mentionedID] = struct{}{}
		}
		attachments, err := messageattachment.Encode(input.Attachments)
		if err != nil {
			return err
		}
		var parent any
		if input.ParentReplyID != "" {
			parent = input.ParentReplyID
		}
		if _, err := current.DB().Exec(ctx, `
			INSERT INTO public.forum_replies (
				id, post_id, parent_reply_id, author_id, content, attachments, status, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6::jsonb, 'active', $7, $7)`,
			replyID, postID, parent, actorID, input.Content, attachments, now); err != nil {
			return err
		}
		if _, err := current.DB().Exec(ctx, `UPDATE public.forum_posts SET updated_at = $2 WHERE id = $1`, postID, now); err != nil {
			return err
		}
		actorName, err := current.userDisplayName(ctx, actorID)
		if err != nil {
			return err
		}
		recipients := make(map[string]string)
		if postAuthorID != actorID {
			recipients[postAuthorID] = "reply"
		}
		if parentAuthorID != "" && parentAuthorID != actorID {
			recipients[parentAuthorID] = "reply"
		}
		for mentionedID := range validMentionIDs {
			if mentionedID != actorID {
				recipients[mentionedID] = "mention"
			}
		}
		for recipientID, eventType := range recipients {
			if err := current.insertNotification(ctx, recipientID, actorID, eventType,
				eventType+":"+replyID, postID, replyID, postTitle,
				actorName+notificationVerb(eventType)+postTitle+"》", now); err != nil {
				return err
			}
		}
		replies, err := current.listReplies(ctx, postID, actorID, role, "")
		if err != nil {
			return err
		}
		for _, reply := range replies {
			if reply.ID == replyID {
				result = reply
				break
			}
		}
		return nil
	})
	return result, found, err
}

func (r ForumRepository) UpdateReply(ctx context.Context, postID, replyID, actorID string, now time.Time, input forumapp.UpdateReplyInput) (forumapp.Reply, bool, bool, error) {
	var result forumapp.Reply
	found := false
	allowed := false
	err := withRepositoryTx(ctx, "forum reply update", r.Repository, func(base Repository) ForumRepository {
		return ForumRepository{Repository: base}
	}, func(current ForumRepository) error {
		// Lock the parent post before the reply everywhere. DeletePost and the
		// other reply mutations use the same order, so a reply cannot be edited
		// after a concurrent soft delete has committed.
		var lockedPostID string
		if err := current.DB().QueryRow(ctx, `
			SELECT id FROM public.forum_posts
			WHERE id = $1 AND status IN ('open', 'resolved')
			FOR UPDATE`, postID).Scan(&lockedPostID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		var authorID string
		if err := current.DB().QueryRow(ctx, `
			SELECT author_id
			FROM public.forum_replies
			WHERE id = $1 AND post_id = $2 AND status = 'active'
			FOR UPDATE`, replyID, postID).Scan(&authorID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		found = true
		if authorID != actorID {
			return nil
		}
		allowed = true
		var content, attachments any
		if input.Content != nil {
			content = *input.Content
		}
		if input.Attachments != nil {
			encoded, err := messageattachment.Encode(*input.Attachments)
			if err != nil {
				return err
			}
			attachments = encoded
		}
		if _, err := current.DB().Exec(ctx, `
			UPDATE public.forum_replies
			SET content = COALESCE($3, content),
				attachments = COALESCE($4::jsonb, attachments),
				updated_at = $5
			WHERE id = $1 AND post_id = $2`, replyID, postID, content, attachments, now); err != nil {
			return err
		}
		item, ok, err := current.getReply(ctx, postID, replyID, actorID, user.RoleStudent, false, "")
		if err != nil {
			return err
		}
		if !ok {
			return forumapp.ErrNotFound
		}
		result = item
		result.CanEdit = true
		result.CanDelete = true
		return nil
	})
	return result, found, allowed, err
}

func (r ForumRepository) DeleteReply(ctx context.Context, postID, replyID, actorID string, role user.Role, now time.Time) (bool, bool, error) {
	found := false
	allowed := false
	err := withRepositoryTx(ctx, "forum reply delete", r.Repository, func(base Repository) ForumRepository {
		return ForumRepository{Repository: base}
	}, func(current ForumRepository) error {
		var acceptedReplyID pgtype.Text
		if err := current.DB().QueryRow(ctx, `
			SELECT accepted_reply_id
			FROM public.forum_posts
			WHERE id = $1
			  AND (status IN ('open', 'resolved') OR ($2::boolean AND status = 'hidden'))
			FOR UPDATE`, postID, role == user.RoleAdmin).Scan(&acceptedReplyID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		var authorID string
		if err := current.DB().QueryRow(ctx, `
			SELECT author_id
			FROM public.forum_replies
			WHERE id = $1 AND post_id = $2
			  AND (status = 'active' OR ($3::boolean AND status = 'hidden'))
			FOR UPDATE`, replyID, postID, role == user.RoleAdmin).Scan(&authorID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		found = true
		if authorID != actorID && role != user.RoleAdmin {
			return nil
		}
		allowed = true
		if _, err := current.DB().Exec(ctx, `
			UPDATE public.forum_replies
			SET status = 'deleted', deleted_at = $3, updated_at = $3
			WHERE id = $1 AND post_id = $2`, replyID, postID, now); err != nil {
			return err
		}
		if _, err := current.DB().Exec(ctx, `
			UPDATE public.forum_replies
			SET parent_reply_id = NULL, updated_at = $2
			WHERE parent_reply_id = $1`, replyID, now); err != nil {
			return err
		}
		if acceptedReplyID.Valid && acceptedReplyID.String == replyID {
			if _, err := current.DB().Exec(ctx, `
				UPDATE public.forum_posts
				SET accepted_reply_id = NULL,
					status = CASE WHEN status = 'hidden' THEN 'hidden' ELSE 'open' END,
					updated_at = $2
				WHERE id = $1`, postID, now); err != nil {
				return err
			}
		} else if _, err := current.DB().Exec(ctx, `
			UPDATE public.forum_posts SET updated_at = $2 WHERE id = $1`, postID, now); err != nil {
			return err
		}
		if role == user.RoleAdmin {
			if _, err := current.DB().Exec(ctx, `
				UPDATE public.forum_reports
				SET status = 'resolved', reviewed_by = $2, reviewed_at = $3
				WHERE target_type = 'reply' AND target_id = $1 AND status = 'pending'`, replyID, actorID, now); err != nil {
				return err
			}
		}
		if _, err := current.DB().Exec(ctx, `
			UPDATE public.forum_notifications
			SET read_at = COALESCE(read_at, $2)
			WHERE reply_id = $1 AND read_at IS NULL`, replyID, now); err != nil {
			return err
		}
		return nil
	})
	return found, allowed, err
}

func (r ForumRepository) SetPostLike(ctx context.Context, postID, actorID string, _ user.Role, active bool, now time.Time) (forumapp.InteractionResult, bool, error) {
	result := forumapp.InteractionResult{Active: active}
	found := false
	err := withRepositoryTx(ctx, "forum post like", r.Repository, func(base Repository) ForumRepository {
		return ForumRepository{Repository: base}
	}, func(current ForumRepository) error {
		var authorID, title string
		if err := current.DB().QueryRow(ctx, `
			SELECT author_id, title FROM public.forum_posts
			WHERE id = $1 AND status IN ('open', 'resolved') FOR UPDATE`, postID).Scan(&authorID, &title); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		found = true
		created := false
		if active {
			tag, err := current.DB().Exec(ctx, `
					INSERT INTO public.forum_post_likes (post_id, user_id, created_at)
				VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, postID, actorID, now)
			if err != nil {
				return err
			}
			created = tag.RowsAffected() > 0
		} else {
			if _, err := current.DB().Exec(ctx, `DELETE FROM public.forum_post_likes WHERE post_id = $1 AND user_id = $2`, postID, actorID); err != nil {
				return err
			}
			// A withdrawn like must not remain unread, and deleting the event lets
			// a later, genuinely new like create a fresh notification.
			if _, err := current.DB().Exec(ctx, `
					DELETE FROM public.forum_notifications
					WHERE recipient_id = $1 AND event_type = 'like' AND event_key = $2`,
				authorID, "like:"+postID+":"+actorID); err != nil {
				return err
			}
		}
		if created && authorID != actorID {
			actorName, err := current.userDisplayName(ctx, actorID)
			if err != nil {
				return err
			}
			if err := current.insertNotification(ctx, authorID, actorID, "like", "like:"+postID+":"+actorID,
				postID, "", title, actorName+"赞了你的帖子《"+title+"》", now); err != nil {
				return err
			}
		}
		return current.DB().QueryRow(ctx, `SELECT COUNT(*) FROM public.forum_post_likes WHERE post_id = $1`, postID).Scan(&result.Count)
	})
	return result, found, err
}

func (r ForumRepository) SetPostFavorite(ctx context.Context, postID, actorID string, active bool, now time.Time) (forumapp.InteractionResult, bool, error) {
	result := forumapp.InteractionResult{Active: active}
	found := false
	err := withRepositoryTx(ctx, "forum post favorite", r.Repository, func(base Repository) ForumRepository {
		return ForumRepository{Repository: base}
	}, func(current ForumRepository) error {
		var lockedPostID string
		if err := current.DB().QueryRow(ctx, `
			SELECT id FROM public.forum_posts
			WHERE id = $1 AND status IN ('open', 'resolved')
			FOR UPDATE`, postID).Scan(&lockedPostID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		found = true
		if active {
			_, err := current.DB().Exec(ctx, `
				INSERT INTO public.forum_post_favorites (post_id, user_id, created_at)
				VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, postID, actorID, now)
			if err != nil {
				return err
			}
		} else if _, err := current.DB().Exec(ctx, `DELETE FROM public.forum_post_favorites WHERE post_id = $1 AND user_id = $2`, postID, actorID); err != nil {
			return err
		}
		return current.DB().QueryRow(ctx, `SELECT COUNT(*) FROM public.forum_post_favorites WHERE post_id = $1`, postID).Scan(&result.Count)
	})
	return result, found, err
}

func (r ForumRepository) AcceptReply(ctx context.Context, postID, replyID, actorID string, role user.Role, now time.Time) (forumapp.PostDetail, bool, bool, error) {
	var result forumapp.PostDetail
	found := false
	allowed := false
	err := withRepositoryTx(ctx, "forum answer acceptance", r.Repository, func(base Repository) ForumRepository {
		return ForumRepository{Repository: base}
	}, func(current ForumRepository) error {
		var authorID, title, currentReplyID string
		var accepted pgtype.Text
		if err := current.DB().QueryRow(ctx, `
			SELECT author_id, title, accepted_reply_id FROM public.forum_posts
			WHERE id = $1 AND status IN ('open', 'resolved') FOR UPDATE`, postID).Scan(&authorID, &title, &accepted); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		found = true
		if authorID != actorID {
			return nil
		}
		allowed = true
		var replyAuthorID string
		if err := current.DB().QueryRow(ctx, `
			SELECT author_id FROM public.forum_replies
			WHERE id = $1 AND post_id = $2 AND status = 'active'`, replyID, postID).Scan(&replyAuthorID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				found = false
				return nil
			}
			return err
		}
		if accepted.Valid {
			currentReplyID = accepted.String
		}
		if currentReplyID != "" && currentReplyID != replyID {
			// The previous answer is no longer accepted. Withdraw its event so it
			// cannot keep contributing to unread counts and can be emitted again if
			// that answer is accepted in the future.
			if _, err := current.DB().Exec(ctx, `
					DELETE FROM public.forum_notifications
					WHERE event_type = 'accepted' AND post_id = $1 AND reply_id = $2`,
				postID, currentReplyID); err != nil {
				return err
			}
		}
		if _, err := current.DB().Exec(ctx, `
			UPDATE public.forum_posts
			SET accepted_reply_id = $2, status = 'resolved', updated_at = $3
			WHERE id = $1`, postID, replyID, now); err != nil {
			return err
		}
		if currentReplyID != replyID && replyAuthorID != actorID {
			actorName, err := current.userDisplayName(ctx, actorID)
			if err != nil {
				return err
			}
			if err := current.insertNotification(ctx, replyAuthorID, actorID, "accepted", "accepted:"+postID+":"+replyID,
				postID, replyID, title, actorName+"采纳了你在《"+title+"》中的回答", now); err != nil {
				return err
			}
		}
		item, ok, err := current.GetPost(ctx, postID, actorID, role, false)
		if err != nil {
			return err
		}
		if ok {
			result = item
		}
		return nil
	})
	return result, found, allowed, err
}

func (r ForumRepository) SetFeatured(ctx context.Context, postID, actorID string, role user.Role, active bool, now time.Time) (forumapp.PostDetail, bool, bool, error) {
	var result forumapp.PostDetail
	found := false
	allowed := false
	err := withRepositoryTx(ctx, "forum post feature", r.Repository, func(base Repository) ForumRepository {
		return ForumRepository{Repository: base}
	}, func(current ForumRepository) error {
		var authorID, title string
		var wasFeatured bool
		var featuredBy pgtype.Text
		if err := current.DB().QueryRow(ctx, `
			SELECT author_id, title, is_featured, featured_by FROM public.forum_posts
			WHERE id = $1 AND status IN ('open', 'resolved') FOR UPDATE`, postID).Scan(&authorID, &title, &wasFeatured, &featuredBy); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		found = true
		if role != user.RoleTeacher {
			return nil
		}
		replacedSelection := wasFeatured && (!featuredBy.Valid || featuredBy.String != actorID)
		if !active {
			// Selection belongs to the teacher who created it. That teacher can
			// revoke it even after the student leaves their class.
			if !wasFeatured || !featuredBy.Valid || featuredBy.String != actorID {
				return nil
			}
			tag, err := current.DB().Exec(ctx, `
				UPDATE public.forum_posts
				SET is_featured = false, featured_by = NULL, featured_at = NULL
				WHERE id = $1 AND featured_by = $2`, postID, actorID)
			if err != nil {
				return err
			}
			allowed = tag.RowsAffected() > 0
		} else {
			// A current teacher may select their own student. Keep another
			// teacher's selection while that teacher still owns the student's
			// current class; once ownership is stale, the current teacher may
			// replace it so the post does not remain permanently unselectable.
			if replacedSelection && featuredBy.Valid {
				var ownerStillTeaches bool
				if err := current.DB().QueryRow(ctx, `
					SELECT EXISTS (
						SELECT 1
						FROM public.class_enrollments enrollment
						JOIN public.classes cls ON cls.id = enrollment.class_id
						WHERE enrollment.student_id = $1 AND cls.teacher_id = $2
					)`, authorID, featuredBy.String).Scan(&ownerStillTeaches); err != nil {
					return err
				}
				if ownerStillTeaches {
					return nil
				}
				replacedSelection = true
			}
			tag, err := current.DB().Exec(ctx, `
				UPDATE public.forum_posts AS target SET
					is_featured = true,
					featured_by = $2,
					featured_at = CASE WHEN target.is_featured AND target.featured_by = $2 THEN target.featured_at ELSE $3 END
				WHERE target.id = $1
				  AND EXISTS (
					SELECT 1
					FROM public.users student
					JOIN public.class_enrollments enrollment ON enrollment.student_id = student.id
					JOIN public.classes cls ON cls.id = enrollment.class_id
					WHERE student.id = target.author_id
					  AND student.role::text = 'STUDENT'
					  AND cls.teacher_id = $2
				  )`, postID, actorID, now)
			if err != nil {
				return err
			}
			allowed = tag.RowsAffected() > 0
		}
		if !allowed {
			return nil
		}
		if !active || !wasFeatured || replacedSelection {
			// Only one teacher selection can be current for a post. Withdraw the old
			// notification whenever that selection is removed or replaced.
			if _, err := current.DB().Exec(ctx, `
				DELETE FROM public.forum_notifications
				WHERE event_type = 'featured' AND post_id = $1`, postID); err != nil {
				return err
			}
		}
		if active && (!wasFeatured || replacedSelection) && authorID != actorID {
			actorName, err := current.userDisplayName(ctx, actorID)
			if err != nil {
				return err
			}
			if err := current.insertNotification(ctx, authorID, actorID, "featured", "featured:"+postID+":"+actorID,
				postID, "", title, actorName+"将你的帖子《"+title+"》设为精选", now); err != nil {
				return err
			}
		}
		item, ok, err := current.GetPost(ctx, postID, actorID, role, false)
		if err != nil {
			return err
		}
		if ok {
			result = item
		}
		return nil
	})
	return result, found, allowed, err
}

func (r ForumRepository) CreateReport(ctx context.Context, reportID, reporterID, targetType, targetID, reason, detail string, now time.Time) (forumapp.Report, bool, error) {
	var item forumapp.Report
	found := false
	err := withRepositoryTx(ctx, "forum report create", r.Repository, func(base Repository) ForumRepository {
		return ForumRepository{Repository: base}
	}, func(current ForumRepository) error {
		var authorID, postID string
		var err error
		found, authorID, postID, err = current.lockReportTarget(ctx, targetType, targetID)
		if err != nil || !found {
			return err
		}
		if authorID == reporterID {
			return forumapp.ErrForbidden
		}
		row := current.DB().QueryRow(ctx, `
			INSERT INTO public.forum_reports (id, reporter_id, target_type, target_id, reason, detail, status, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7)
			ON CONFLICT (reporter_id, target_type, target_id) WHERE status = 'pending' DO NOTHING
			RETURNING id, target_type, target_id, reason, detail, status, reviewed_by, reviewed_at, created_at`,
			reportID, reporterID, targetType, targetID, reason, detail, now)
		item = forumapp.Report{Reporter: forumapp.Author{ID: reporterID}, PostID: postID}
		var reviewedBy pgtype.Text
		var reviewedAt pgtype.Timestamp
		if err := row.Scan(&item.ID, &item.TargetType, &item.TargetID, &item.Reason, &item.Detail,
			&item.Status, &reviewedBy, &reviewedAt, &item.CreatedAt); errors.Is(err, pgx.ErrNoRows) {
			item = forumapp.Report{}
			return nil
		} else if err != nil {
			return err
		}
		if reviewedBy.Valid {
			item.ReviewedBy = reviewedBy.String
		}
		if reviewedAt.Valid {
			value := messageCenterWallTime(reviewedAt.Time)
			item.ReviewedAt = &value
		}
		item.CreatedAt = messageCenterWallTime(item.CreatedAt)
		return nil
	})
	return item, found, err
}

const forumNotificationTargetActiveSQL = `EXISTS (
	SELECT 1
	FROM public.forum_posts notification_post
	LEFT JOIN public.forum_replies notification_reply
		ON notification_reply.id = n.reply_id
		AND notification_reply.post_id = notification_post.id
	WHERE notification_post.id = n.post_id
	  AND notification_post.status IN ('open', 'resolved')
	  AND (n.reply_id IS NULL OR notification_reply.status = 'active')
	  AND CASE n.event_type
		WHEN 'like' THEN EXISTS (
			SELECT 1
			FROM public.forum_post_likes notification_like
			WHERE notification_like.post_id = n.post_id
			  AND notification_like.user_id = n.actor_id
		)
		WHEN 'accepted' THEN notification_post.accepted_reply_id = n.reply_id
		WHEN 'featured' THEN notification_post.is_featured
			AND notification_post.featured_by = n.actor_id
			AND EXISTS (
				SELECT 1
				FROM public.class_enrollments notification_enrollment
				JOIN public.classes notification_class
					ON notification_class.id = notification_enrollment.class_id
				WHERE notification_enrollment.student_id = notification_post.author_id
				  AND notification_class.teacher_id = notification_post.featured_by
			)
		ELSE true
	  END
)`

func (r ForumRepository) ListNotifications(ctx context.Context, recipientID string, unreadOnly bool, page, pageSize int) ([]forumapp.Notification, int, int, error) {
	where := ` WHERE n.recipient_id = $1`
	if unreadOnly {
		where += ` AND n.read_at IS NULL AND ` + forumNotificationTargetActiveSQL
	}
	var total, unread int
	if err := r.DB().QueryRow(ctx, `SELECT COUNT(*) FROM public.forum_notifications n`+where, recipientID).Scan(&total); err != nil {
		return nil, 0, 0, err
	}
	if err := r.DB().QueryRow(ctx, `
		SELECT COUNT(*)
		FROM public.forum_notifications n
		WHERE n.recipient_id = $1
		  AND n.read_at IS NULL
		  AND `+forumNotificationTargetActiveSQL, recipientID).Scan(&unread); err != nil {
		return nil, 0, 0, err
	}
	rows, err := r.DB().Query(ctx, `
		SELECT n.id, n.event_type, n.post_id, n.reply_id, n.title, n.summary,
			COALESCE(n.read_at, CASE WHEN NOT `+forumNotificationTargetActiveSQL+` THEN n.created_at END),
			n.created_at,
			a.id, COALESCE(NULLIF(BTRIM(a.display_name), ''), a.username), a.role::text, a.avatar_url
		FROM public.forum_notifications n
		LEFT JOIN public.users a ON a.id = n.actor_id`+where+`
		ORDER BY n.created_at DESC, n.id DESC
		LIMIT $2 OFFSET $3`, recipientID, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()
	items := make([]forumapp.Notification, 0)
	for rows.Next() {
		item, err := scanForumNotification(rows)
		if err != nil {
			return nil, 0, 0, err
		}
		items = append(items, item)
	}
	return items, total, unread, rows.Err()
}

func (r ForumRepository) MarkNotificationRead(ctx context.Context, notificationID, recipientID string, now time.Time) (bool, error) {
	tag, err := r.DB().Exec(ctx, `
		UPDATE public.forum_notifications
		SET read_at = COALESCE(read_at, $3)
		WHERE id = $1 AND recipient_id = $2`, notificationID, recipientID, now)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r ForumRepository) MarkPostNotificationsRead(ctx context.Context, postID, recipientID string, now time.Time) (int, error) {
	tag, err := r.DB().Exec(ctx, `
		UPDATE public.forum_notifications SET read_at = $3
		WHERE post_id = $1 AND recipient_id = $2 AND read_at IS NULL`, postID, recipientID, now)
	return int(tag.RowsAffected()), err
}

func (r ForumRepository) MarkAllNotificationsRead(ctx context.Context, recipientID string, now time.Time) (int, error) {
	tag, err := r.DB().Exec(ctx, `
		UPDATE public.forum_notifications SET read_at = $2
		WHERE recipient_id = $1 AND read_at IS NULL`, recipientID, now)
	return int(tag.RowsAffected()), err
}

func (r ForumRepository) ListUnreadNotificationPostIDs(ctx context.Context, recipientID string) ([]string, error) {
	rows, err := r.DB().Query(ctx, `
		SELECT n.post_id
		FROM public.forum_notifications n
		WHERE n.recipient_id = $1
		  AND n.read_at IS NULL
		  AND n.post_id IS NOT NULL
		  AND `+forumNotificationTargetActiveSQL+`
		GROUP BY n.post_id
		ORDER BY MAX(n.created_at) DESC, n.post_id DESC`, recipientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]string, 0)
	for rows.Next() {
		var postID string
		if err := rows.Scan(&postID); err != nil {
			return nil, err
		}
		items = append(items, postID)
	}
	return items, rows.Err()
}

func (r ForumRepository) ListReports(ctx context.Context, status string, page, pageSize int) ([]forumapp.Report, int, error) {
	where := ""
	args := []any{}
	if status != "all" {
		where = ` WHERE r.status = $1`
		args = append(args, status)
	}
	var total int
	if err := r.DB().QueryRow(ctx, `SELECT COUNT(*) FROM public.forum_reports r`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, pageSize, (page-1)*pageSize)
	limitIndex := len(args) - 1
	rows, err := r.DB().Query(ctx, `
		SELECT r.id, r.target_type, r.target_id,
			CASE WHEN r.target_type = 'post' THEN r.target_id ELSE reply.post_id END AS post_id,
			r.reason, r.detail, r.status, r.reviewed_by, r.reviewed_at, r.created_at,
			u.id, COALESCE(NULLIF(BTRIM(u.display_name), ''), u.username), u.role::text, u.avatar_url
		FROM public.forum_reports r
		JOIN public.users u ON u.id = r.reporter_id
		LEFT JOIN public.forum_replies reply ON r.target_type = 'reply' AND reply.id = r.target_id`+where+`
		ORDER BY r.created_at DESC, r.id DESC
		LIMIT $`+idxStr(limitIndex)+` OFFSET $`+idxStr(limitIndex+1), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]forumapp.Report, 0)
	for rows.Next() {
		item, err := scanForumReport(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (r ForumRepository) ResolveReport(ctx context.Context, reportID, reviewerID, status string, now time.Time) (forumapp.Report, bool, error) {
	row := r.DB().QueryRow(ctx, `
		WITH updated AS (
			UPDATE public.forum_reports
			SET status = $2, reviewed_by = $3, reviewed_at = $4
			WHERE id = $1 AND status = 'pending'
			RETURNING id, reporter_id, target_type, target_id, reason, detail,
				status, reviewed_by, reviewed_at, created_at
		)
		SELECT updated.id, updated.target_type, updated.target_id,
			CASE WHEN updated.target_type = 'post' THEN updated.target_id ELSE reply.post_id END,
			updated.reason, updated.detail, updated.status, updated.reviewed_by,
			updated.reviewed_at, updated.created_at,
			reporter.id, COALESCE(NULLIF(BTRIM(reporter.display_name), ''), reporter.username),
			reporter.role::text, reporter.avatar_url
		FROM updated
		JOIN public.users reporter ON reporter.id = updated.reporter_id
		LEFT JOIN public.forum_replies reply
			ON updated.target_type = 'reply' AND reply.id = updated.target_id`,
		reportID, status, reviewerID, now)
	item, err := scanForumReport(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return forumapp.Report{}, false, nil
	}
	if err != nil {
		return forumapp.Report{}, false, err
	}
	return item, true, nil
}

// forumFeaturedForViewerSQL scopes a teacher's selection to the post author's
// current class. The post remains globally readable; only its featured state is scoped.
const forumFeaturedForViewerSQL = `(
	p.is_featured
	AND p.featured_by IS NOT NULL
	AND EXISTS (
		SELECT 1
		FROM public.users forum_viewer
		WHERE forum_viewer.id = $1
		  AND forum_viewer.role::text IN ('STUDENT', 'TEACHER')
	)
	AND EXISTS (
		SELECT 1
		FROM public.class_enrollments author_enrollment
		JOIN public.classes featured_class ON featured_class.id = author_enrollment.class_id
		WHERE author_enrollment.student_id = p.author_id
		  AND featured_class.teacher_id = p.featured_by
		  AND (
			featured_class.teacher_id = $1
			OR EXISTS (
				SELECT 1
				FROM public.class_enrollments viewer_enrollment
				WHERE viewer_enrollment.student_id = $1
				  AND viewer_enrollment.class_id = author_enrollment.class_id
			)
		  )
	)
)`

// forumViewerCanFeatureSQL mirrors the action available in the scoped UI: a
// current teacher may create or revoke their selection, or replace a selection
// whose original teacher no longer teaches the author.
const forumViewerCanFeatureSQL = `(
	EXISTS (
		SELECT 1
		FROM public.class_enrollments viewer_author_enrollment
		JOIN public.classes viewer_author_class
			ON viewer_author_class.id = viewer_author_enrollment.class_id
		WHERE viewer_author_enrollment.student_id = p.author_id
		  AND author.role::text = 'STUDENT'
		  AND viewer_author_class.teacher_id = $1
	)
	AND (
		NOT p.is_featured
		OR p.featured_by IS NULL
		OR p.featured_by = $1
		OR NOT EXISTS (
			SELECT 1
			FROM public.class_enrollments featured_owner_enrollment
			JOIN public.classes featured_owner_class
				ON featured_owner_class.id = featured_owner_enrollment.class_id
			WHERE featured_owner_enrollment.student_id = p.author_id
			  AND featured_owner_class.teacher_id = p.featured_by
		)
	)
)`

const forumPostSelect = `
	SELECT p.id,
		b.id, b.slug, b.name, b.description, b.sort_order,
		p.post_type, p.title, LEFT(p.content, 300), p.content, p.attachments, p.tags,
		p.knowledge_node_id, kn.name,
		author.id, COALESCE(NULLIF(BTRIM(author.display_name), ''), author.username), author.role::text, author.avatar_url,
		p.status, ` + forumFeaturedForViewerSQL + ` AS is_featured_for_viewer, p.accepted_reply_id,
		(SELECT COUNT(*) FROM public.forum_replies fr WHERE fr.post_id = p.id AND fr.status = 'active'),
		(SELECT COUNT(*) FROM public.forum_post_likes fl WHERE fl.post_id = p.id),
		(SELECT COUNT(*) FROM public.forum_post_favorites ff WHERE ff.post_id = p.id),
		p.view_count,
		EXISTS (SELECT 1 FROM public.forum_post_likes fl WHERE fl.post_id = p.id AND fl.user_id = $1),
		EXISTS (SELECT 1 FROM public.forum_post_favorites ff WHERE ff.post_id = p.id AND ff.user_id = $1),
		` + forumViewerCanFeatureSQL + `,
		p.created_at, p.updated_at`

func scanForumPost(row interface{ Scan(...any) error }, viewerID string, viewerRole user.Role) (forumapp.Post, error) {
	var item forumapp.Post
	var attachmentsRaw, tagsRaw []byte
	var knowledgeID, knowledgeName, avatarURL, acceptedReplyID pgtype.Text
	var authorRole string
	var viewerCanFeature bool
	if err := row.Scan(
		&item.ID,
		&item.Board.ID, &item.Board.Slug, &item.Board.Name, &item.Board.Description, &item.Board.SortOrder,
		&item.Type, &item.Title, &item.Excerpt, &item.Content, &attachmentsRaw, &tagsRaw,
		&knowledgeID, &knowledgeName,
		&item.Author.ID, &item.Author.Name, &authorRole, &avatarURL,
		&item.Status, &item.IsFeatured, &acceptedReplyID,
		&item.ReplyCount, &item.LikeCount, &item.FavoriteCount, &item.ViewCount,
		&item.IsLiked, &item.IsFavorited, &viewerCanFeature, &item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return forumapp.Post{}, err
	}
	role, err := user.ParseRole(authorRole)
	if err != nil {
		return forumapp.Post{}, err
	}
	item.Author.Role = role
	if avatarURL.Valid {
		item.Author.AvatarURL = avatarURL.String
	}
	if knowledgeID.Valid {
		item.KnowledgeNodeID = knowledgeID.String
	}
	if knowledgeName.Valid {
		item.KnowledgeNodeName = knowledgeName.String
	}
	if acceptedReplyID.Valid {
		item.AcceptedReplyID = acceptedReplyID.String
	}
	item.Attachments, err = messageattachment.Decode(attachmentsRaw)
	if err != nil {
		return forumapp.Post{}, err
	}
	item.Tags, err = decodeStringSlice(tagsRaw)
	if err != nil {
		return forumapp.Post{}, err
	}
	item.Category = item.Board.Slug
	isOwner := item.Author.ID == viewerID
	isActive := item.Status == "open" || item.Status == "resolved"
	isAdminDeletable := viewerRole == user.RoleAdmin && item.Status == "hidden"
	item.Permissions = forumapp.Permissions{
		CanEdit:         isActive && isOwner,
		CanDelete:       (isActive || isAdminDeletable) && (isOwner || viewerRole == user.RoleAdmin),
		CanAcceptAnswer: isActive && isOwner && item.ReplyCount > 0,
		CanFeature:      isActive && viewerRole == user.RoleTeacher && viewerCanFeature,
		CanReport:       isActive && !isOwner,
	}
	item.CreatedAt = messageCenterWallTime(item.CreatedAt)
	item.UpdatedAt = messageCenterWallTime(item.UpdatedAt)
	return item, nil
}

func (r ForumRepository) listReplies(ctx context.Context, postID, viewerID string, viewerRole user.Role, acceptedReplyID string) ([]forumapp.Reply, error) {
	rows, err := r.DB().Query(ctx, `
		SELECT reply.id, reply.post_id, reply.parent_reply_id,
			author.id, COALESCE(NULLIF(BTRIM(author.display_name), ''), author.username), author.role::text, author.avatar_url,
			reply.content, reply.attachments, reply.status, reply.created_at, reply.updated_at
		FROM public.forum_replies reply
		JOIN public.users author ON author.id = reply.author_id
		WHERE reply.post_id = $1 AND (reply.status = 'active' OR $2::boolean)
		ORDER BY reply.created_at, reply.id`, postID, viewerRole == user.RoleAdmin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]forumapp.Reply, 0)
	for rows.Next() {
		item, err := scanForumReply(rows, viewerID, viewerRole, acceptedReplyID)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r ForumRepository) getReply(ctx context.Context, postID, replyID, viewerID string, viewerRole user.Role, includeDeleted bool, acceptedReplyID string) (forumapp.Reply, bool, error) {
	row := r.DB().QueryRow(ctx, `
		SELECT reply.id, reply.post_id, reply.parent_reply_id,
			author.id, COALESCE(NULLIF(BTRIM(author.display_name), ''), author.username), author.role::text, author.avatar_url,
			reply.content, reply.attachments, reply.status, reply.created_at, reply.updated_at
		FROM public.forum_replies reply
		JOIN public.users author ON author.id = reply.author_id
		WHERE reply.id = $1 AND reply.post_id = $2
		  AND (reply.status = 'active' OR $3::boolean)`, replyID, postID, includeDeleted)
	item, err := scanForumReply(row, viewerID, viewerRole, acceptedReplyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return forumapp.Reply{}, false, nil
	}
	return item, err == nil, err
}

func scanForumReply(row interface{ Scan(...any) error }, viewerID string, viewerRole user.Role, acceptedReplyID string) (forumapp.Reply, error) {
	var item forumapp.Reply
	var parentID, avatarURL pgtype.Text
	var authorRole string
	var attachmentsRaw []byte
	if err := row.Scan(&item.ID, &item.PostID, &parentID,
		&item.Author.ID, &item.Author.Name, &authorRole, &avatarURL,
		&item.Content, &attachmentsRaw, &item.Status, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return forumapp.Reply{}, err
	}
	role, err := user.ParseRole(authorRole)
	if err != nil {
		return forumapp.Reply{}, err
	}
	item.Author.Role = role
	if parentID.Valid {
		item.ParentReplyID = parentID.String
	}
	if avatarURL.Valid {
		item.Author.AvatarURL = avatarURL.String
	}
	item.Attachments, err = messageattachment.Decode(attachmentsRaw)
	if err != nil {
		return forumapp.Reply{}, err
	}
	isActive := item.Status == "active"
	isAdminDeletable := viewerRole == user.RoleAdmin && item.Status == "hidden"
	item.IsAccepted = isActive && item.ID == acceptedReplyID
	item.CanEdit = isActive && item.Author.ID == viewerID
	item.CanDelete = (isActive || isAdminDeletable) && (item.Author.ID == viewerID || viewerRole == user.RoleAdmin)
	item.CanReport = isActive && item.Author.ID != viewerID
	item.CreatedAt = messageCenterWallTime(item.CreatedAt)
	item.UpdatedAt = messageCenterWallTime(item.UpdatedAt)
	return item, nil
}

func (r ForumRepository) resolveUpdateBoard(ctx context.Context, input forumapp.UpdatePostInput) (any, error) {
	if input.BoardID == nil && input.BoardSlug == nil {
		return nil, nil
	}
	var boardID string
	var err error
	if input.BoardID != nil {
		err = r.DB().QueryRow(ctx, `SELECT id FROM public.forum_boards WHERE id = $1 AND is_active = true`, *input.BoardID).Scan(&boardID)
	} else {
		err = r.DB().QueryRow(ctx, `SELECT id FROM public.forum_boards WHERE slug = $1 AND is_active = true`, *input.BoardSlug).Scan(&boardID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, forumapp.ErrInvalidInput
	}
	return boardID, err
}

func (r ForumRepository) userDisplayName(ctx context.Context, userID string) (string, error) {
	var name string
	err := r.DB().QueryRow(ctx, `
		SELECT COALESCE(NULLIF(BTRIM(display_name), ''), username)
		FROM public.users WHERE id = $1 AND is_active = true`, userID).Scan(&name)
	return name, err
}

func (r ForumRepository) insertNotification(ctx context.Context, recipientID, actorID, eventType, eventKey, postID, replyID, title, summary string, now time.Time) error {
	if recipientID == actorID {
		return nil
	}
	notificationID, err := newUUID()
	if err != nil {
		return err
	}
	var reply any
	if replyID != "" {
		reply = replyID
	}
	_, err = r.DB().Exec(ctx, `
		INSERT INTO public.forum_notifications (
			id, recipient_id, actor_id, event_type, event_key, post_id, reply_id,
			title, summary, created_at
		)
		SELECT $1, recipient.id, $3, $4, $5, $6, $7, LEFT($8, 200), LEFT($9, 500), $10
		FROM public.users recipient
		WHERE recipient.id = $2 AND recipient.is_active = true
		ON CONFLICT (recipient_id, event_key) DO NOTHING`,
		notificationID, recipientID, actorID, eventType, eventKey, postID, reply, title, summary, now)
	return err
}

func notificationVerb(eventType string) string {
	if eventType == "mention" {
		return "在帖子中提及了你：《"
	}
	return "回复了帖子《"
}

func (r ForumRepository) lockReportTarget(ctx context.Context, targetType, targetID string) (bool, string, string, error) {
	var authorID, postID string
	if targetType == "post" {
		err := r.DB().QueryRow(ctx, `
			SELECT author_id, id FROM public.forum_posts
			WHERE id = $1 AND status IN ('open', 'resolved')
			FOR UPDATE`, targetID).Scan(&authorID, &postID)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, "", "", nil
		}
		return err == nil, authorID, postID, err
	}

	// Resolve the post ID first without locking, then acquire locks in the same
	// post -> reply order used by reply mutations. Both active-state predicates
	// are checked again while the locks are held.
	if err := r.DB().QueryRow(ctx, `
		SELECT post_id FROM public.forum_replies WHERE id = $1`, targetID).Scan(&postID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, "", "", nil
		}
		return false, "", "", err
	}
	var lockedPostID string
	if err := r.DB().QueryRow(ctx, `
		SELECT id FROM public.forum_posts
		WHERE id = $1 AND status IN ('open', 'resolved')
		FOR UPDATE`, postID).Scan(&lockedPostID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, "", "", nil
		}
		return false, "", "", err
	}
	err := r.DB().QueryRow(ctx, `
		SELECT author_id FROM public.forum_replies
		WHERE id = $1 AND post_id = $2 AND status = 'active'
		FOR UPDATE`, targetID, postID).Scan(&authorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "", "", nil
	}
	return err == nil, authorID, postID, err
}

func scanForumNotification(row interface{ Scan(...any) error }) (forumapp.Notification, error) {
	var item forumapp.Notification
	var postID, replyID, actorID, actorName, actorRole, avatarURL pgtype.Text
	var readAt pgtype.Timestamp
	if err := row.Scan(&item.ID, &item.EventType, &postID, &replyID, &item.Title, &item.Summary,
		&readAt, &item.CreatedAt, &actorID, &actorName, &actorRole, &avatarURL); err != nil {
		return forumapp.Notification{}, err
	}
	if postID.Valid {
		item.PostID = postID.String
	}
	if replyID.Valid {
		item.ReplyID = replyID.String
	}
	if readAt.Valid {
		value := messageCenterWallTime(readAt.Time)
		item.ReadAt = &value
		item.IsRead = true
	}
	if actorID.Valid {
		actor := forumapp.Author{ID: actorID.String, Name: actorName.String}
		if role, err := user.ParseRole(actorRole.String); err == nil {
			actor.Role = role
		}
		if avatarURL.Valid {
			actor.AvatarURL = avatarURL.String
		}
		item.Actor = &actor
	}
	item.CreatedAt = messageCenterWallTime(item.CreatedAt)
	return item, nil
}

func scanForumReport(row interface{ Scan(...any) error }) (forumapp.Report, error) {
	var item forumapp.Report
	var postID, reviewedBy, avatarURL pgtype.Text
	var reviewedAt pgtype.Timestamp
	var reporterRole string
	if err := row.Scan(&item.ID, &item.TargetType, &item.TargetID, &postID, &item.Reason, &item.Detail,
		&item.Status, &reviewedBy, &reviewedAt, &item.CreatedAt,
		&item.Reporter.ID, &item.Reporter.Name, &reporterRole, &avatarURL); err != nil {
		return forumapp.Report{}, err
	}
	if postID.Valid {
		item.PostID = postID.String
	}
	if reviewedBy.Valid {
		item.ReviewedBy = reviewedBy.String
	}
	if reviewedAt.Valid {
		value := messageCenterWallTime(reviewedAt.Time)
		item.ReviewedAt = &value
	}
	role, err := user.ParseRole(reporterRole)
	if err != nil {
		return forumapp.Report{}, err
	}
	item.Reporter.Role = role
	if avatarURL.Valid {
		item.Reporter.AvatarURL = avatarURL.String
	}
	item.CreatedAt = messageCenterWallTime(item.CreatedAt)
	return item, nil
}
