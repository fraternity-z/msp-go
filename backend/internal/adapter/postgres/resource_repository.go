package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	resourceapp "mathstudy/backend/internal/application/resource"
	"mathstudy/backend/internal/platform/metautil"
	"mathstudy/backend/internal/platform/sliceutil"
	"mathstudy/backend/internal/platform/stringutil"
)

// ResourceRepository persists resource center data in PostgreSQL.
type ResourceRepository struct {
	Repository
}

// NewResourceRepository creates a PostgreSQL-backed resource repository.
func NewResourceRepository(db Querier) (ResourceRepository, error) {
	base, err := NewRepository(db)
	if err != nil {
		return ResourceRepository{}, err
	}
	return ResourceRepository{Repository: base}, nil
}

// ListResources returns published video/document resources and total count.
func (r ResourceRepository) ListResources(ctx context.Context, userID string, filter resourceapp.ListFilter) ([]resourceapp.Resource, int, error) {
	where, args := resourceWhereClause(userID, filter)
	countSQL := `
		SELECT count(c.id)::int
		FROM public.contents c
		WHERE ` + where

	var total int
	if err := r.DB().QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	offset := (filter.Page - 1) * filter.PageSize
	args = append(args, filter.PageSize, offset)
	limitPlaceholder := fmt.Sprintf("$%d", len(args)-1)
	offsetPlaceholder := fmt.Sprintf("$%d", len(args))
	rows, err := r.DB().Query(ctx, `
		SELECT `+resourceSelectColumns+`
		FROM public.contents c
		LEFT JOIN public.users u ON u.id = c.owner_teacher_id
		LEFT JOIN LATERAL (
			SELECT ca.url
			FROM public.content_assets ca
			WHERE ca.content_id = c.id
			ORDER BY ca.created_at ASC, ca.id ASC
			LIMIT 1
		) asset ON true
		LEFT JOIN public.user_favorites uf ON uf.user_id = $1 AND uf.content_id = c.id
		WHERE `+where+`
		ORDER BY c.created_at DESC, c.id DESC
		LIMIT `+limitPlaceholder+` OFFSET `+offsetPlaceholder,
		args...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	resources := []resourceapp.Resource{}
	for rows.Next() {
		resource, err := scanResourceRow(rows)
		if err != nil {
			return nil, 0, err
		}
		resource.Body = ""
		resources = append(resources, resource)
	}
	return resources, total, rows.Err()
}

// GetResourceByID returns one published resource and increments its view counter.
func (r ResourceRepository) GetResourceByID(ctx context.Context, resourceID string, userID string) (resourceapp.Resource, bool, error) {
	resource, ok, _, err := r.getResource(ctx, resourceID, userID, true)
	if err != nil || !ok {
		return resourceapp.Resource{}, ok, err
	}
	var metaRaw []byte
	if err := r.DB().QueryRow(ctx, `
		UPDATE public.contents
		SET meta = jsonb_set(
			CASE
				WHEN jsonb_typeof(meta::jsonb) = 'object' THEN meta::jsonb
				ELSE '{}'::jsonb
			END,
			'{views}',
			CASE
				WHEN jsonb_typeof(meta::jsonb -> 'views') = 'number'
					THEN to_jsonb(trunc((meta ->> 'views')::numeric) + 1)
				ELSE '1'::jsonb
			END,
			true
		)::json
		WHERE id = $1
			AND status = 'PUBLISHED'
			AND deleted_at IS NULL
			AND type IN ('VIDEO', 'ARTICLE')
		RETURNING meta`,
		resourceID,
	).Scan(&metaRaw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return resourceapp.Resource{}, false, nil
		}
		return resourceapp.Resource{}, false, err
	}
	meta, err := decodeObjectMap(metaRaw)
	if err != nil {
		return resourceapp.Resource{}, false, fmt.Errorf("decode incremented resource meta: %w", err)
	}
	resource.Views = metautil.Int(meta, "views")
	return resource, true, nil
}

// CreateResource inserts a published teacher resource and optional asset.
func (r ResourceRepository) CreateResource(ctx context.Context, ownerID string, input resourceapp.ResourceInput, now time.Time) (resourceapp.Resource, error) {
	var resourceID string
	err := r.withTx(ctx, func(tx ResourceRepository) error {
		id, err := tx.insertResource(ctx, ownerID, input, now)
		if err != nil {
			return err
		}
		resourceID = id
		return nil
	})
	if err != nil {
		return resourceapp.Resource{}, err
	}
	resource, ok, _, err := r.getResource(ctx, resourceID, ownerID, false)
	if err != nil {
		return resourceapp.Resource{}, err
	}
	if !ok {
		return resourceapp.Resource{}, pgx.ErrNoRows
	}
	return resource, nil
}

// UpdateResource updates a teacher-owned resource.
func (r ResourceRepository) UpdateResource(ctx context.Context, resourceID string, ownerID string, input resourceapp.ResourceUpdate, now time.Time) (resourceapp.Resource, bool, error) {
	var resource resourceapp.Resource
	var found bool
	err := r.withTx(ctx, func(tx ResourceRepository) error {
		var err error
		resource, found, err = tx.updateResource(ctx, resourceID, ownerID, input, now)
		return err
	})
	if err != nil {
		return resourceapp.Resource{}, false, err
	}
	return resource, found, nil
}

func (r ResourceRepository) updateResource(ctx context.Context, resourceID string, ownerID string, input resourceapp.ResourceUpdate, now time.Time) (resourceapp.Resource, bool, error) {
	var currentType string
	var title string
	var body string
	var difficulty float64
	var tagsRaw []byte
	var metaRaw []byte
	err := r.DB().QueryRow(ctx, `
		SELECT type::text, title, body, difficulty, tags, meta
		FROM public.contents c
		WHERE c.id = $1 AND c.owner_teacher_id = $2 AND c.deleted_at IS NULL
			AND `+resourceContentTypeCondition+`
		FOR UPDATE`,
		resourceID,
		ownerID,
	).Scan(&currentType, &title, &body, &difficulty, &tagsRaw, &metaRaw)
	if err != nil {
		if err == pgx.ErrNoRows {
			return resourceapp.Resource{}, false, nil
		}
		return resourceapp.Resource{}, false, err
	}

	tags, err := decodeStringSlice(tagsRaw)
	if err != nil {
		return resourceapp.Resource{}, false, fmt.Errorf("decode resource tags: %w", err)
	}
	meta, err := decodeObjectMap(metaRaw)
	if err != nil {
		return resourceapp.Resource{}, false, fmt.Errorf("decode resource meta: %w", err)
	}
	if input.Body != nil || input.Type != nil || input.URL != nil || input.StorageType != nil {
		var immutable bool
		var assetURL string
		if err := r.DB().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.resource_documents WHERE resource_id=$1),
			coalesce((SELECT url FROM public.content_assets WHERE content_id=$1 ORDER BY created_at,id LIMIT 1),'')`, resourceID).Scan(&immutable, &assetURL); err != nil {
			return resourceapp.Resource{}, false, err
		}
		if immutable {
			if input.Body != nil && *input.Body != body || input.Type != nil && resourceTypeToDB(*input.Type) != currentType ||
				input.URL != nil && *input.URL != assetURL || input.StorageType != nil && *input.StorageType != metaStringDefault(meta, "storage_type", "external") {
				return resourceapp.Resource{}, false, resourceapp.ErrIngestionConflict
			}
			input.URL = nil
		}
	}

	if input.Title != nil {
		title = *input.Title
	}
	if input.Body != nil {
		body = *input.Body
	}
	if input.Type != nil {
		currentType = resourceTypeToDB(*input.Type)
	}
	if input.Difficulty != nil {
		difficulty = *input.Difficulty
	}
	if input.TagsSet {
		tags = sliceutil.CloneStrings(input.Tags)
	}
	for key, value := range map[string]any{
		"chapter":      input.Chapter,
		"topic":        input.Topic,
		"source":       input.Source,
		"duration":     input.Duration,
		"pages":        input.Pages,
		"storage_type": input.StorageType,
	} {
		switch typed := value.(type) {
		case *string:
			if typed != nil {
				meta[key] = *typed
			}
		case *int:
			if typed != nil {
				meta[key] = *typed
			}
		}
	}

	tagsJSON, err := json.Marshal(tags)
	if err != nil {
		return resourceapp.Resource{}, false, err
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return resourceapp.Resource{}, false, err
	}
	tag, err := r.DB().Exec(ctx, `
		UPDATE public.contents c
		SET type = $3::public.contenttype,
			title = $4,
			body = $5,
			difficulty = $6,
			tags = $7::json,
			meta = $8::json,
			updated_at = $9
		WHERE c.id = $1 AND c.owner_teacher_id = $2 AND c.deleted_at IS NULL
			AND `+resourceContentTypeCondition,
		resourceID,
		ownerID,
		currentType,
		title,
		body,
		difficulty,
		string(tagsJSON),
		string(metaJSON),
		now,
	)
	if err != nil {
		return resourceapp.Resource{}, false, err
	}
	if tag.RowsAffected() == 0 {
		return resourceapp.Resource{}, false, nil
	}
	if input.URL != nil {
		if err := r.replaceResourceAsset(
			ctx,
			resourceID,
			resourceTypeFromDB(currentType),
			metaStringDefault(meta, "storage_type", "external"),
			metautil.StringPointer(meta, "duration"),
			metautil.IntPointer(meta, "pages"),
			input,
			now,
		); err != nil {
			return resourceapp.Resource{}, false, err
		}
	}

	resource, ok, _, err := r.getResource(ctx, resourceID, ownerID, false)
	return resource, ok, err
}

// DeleteResource soft-deletes a teacher-owned resource.
func (r ResourceRepository) DeleteResource(ctx context.Context, resourceID string, ownerID string, now time.Time) (bool, error) {
	var ingested bool
	if err := r.DB().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.resource_documents WHERE resource_id=$1)`, resourceID).Scan(&ingested); err != nil {
		return false, err
	}
	if ingested {
		_, found, err := r.WithdrawIngestion(ctx, ownerID, resourceID, true, now)
		return found, err
	}
	tag, err := r.DB().Exec(ctx, `
		UPDATE public.contents c
		SET deleted_at = $3, status = 'ARCHIVED'::public.contentstatus
		WHERE c.id = $1 AND c.owner_teacher_id = $2 AND c.deleted_at IS NULL
			AND `+resourceContentTypeCondition,
		resourceID,
		ownerID,
		now,
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ToggleFavorite flips a user's favorite row and returns the new state.
func (r ResourceRepository) ToggleFavorite(ctx context.Context, userID string, resourceID string) (bool, bool, error) {
	var isFavorite bool
	var found bool
	err := r.withTx(ctx, func(tx ResourceRepository) error {
		exists, err := tx.Exists(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM public.contents c
				WHERE c.id = $1 AND c.status = 'PUBLISHED' AND c.deleted_at IS NULL
					AND `+resourceContentTypeCondition+`
			)`,
			resourceID,
		)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
		found = true

		tag, err := tx.DB().Exec(ctx, `
			DELETE FROM public.user_favorites
			WHERE user_id = $1 AND content_id = $2`,
			userID,
			resourceID,
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() > 0 {
			isFavorite = false
			return nil
		}

		favoriteID, err := newUUID()
		if err != nil {
			return err
		}
		_, err = tx.DB().Exec(ctx, `
			INSERT INTO public.user_favorites (id, user_id, content_id, created_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (user_id, content_id) DO NOTHING`,
			favoriteID,
			userID,
			resourceID,
			time.Now().UTC(),
		)
		if err != nil {
			return err
		}
		isFavorite = true
		return nil
	})
	if err != nil {
		return false, false, err
	}
	return isFavorite, found, nil
}

// GetStats returns published resource counters and current user's favorite count.
func (r ResourceRepository) GetStats(ctx context.Context, userID string) (resourceapp.Stats, error) {
	var stats resourceapp.Stats
	err := r.DB().QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE c.type IN ('VIDEO', 'ARTICLE', 'NOTE'))::int AS total,
			count(*) FILTER (WHERE c.type = 'VIDEO')::int AS videos,
			count(*) FILTER (WHERE c.type = 'ARTICLE')::int AS documents
		FROM public.contents c
		WHERE c.status = 'PUBLISHED' AND c.deleted_at IS NULL`,
	).Scan(&stats.Total, &stats.Videos, &stats.Documents)
	if err != nil {
		return resourceapp.Stats{}, err
	}
	err = r.DB().QueryRow(ctx, `
		SELECT count(id)::int
		FROM public.user_favorites
		WHERE user_id = $1`,
		userID,
	).Scan(&stats.Favorites)
	if err != nil {
		return resourceapp.Stats{}, err
	}
	return stats, nil
}

func (r ResourceRepository) withTx(ctx context.Context, fn func(ResourceRepository) error) error {
	return withRepositoryTx(ctx, "resource", r.Repository, func(base Repository) ResourceRepository {
		return ResourceRepository{Repository: base}
	}, fn)
}

func (r ResourceRepository) insertResource(ctx context.Context, ownerID string, input resourceapp.ResourceInput, now time.Time) (string, error) {
	resourceID, err := newUUID()
	if err != nil {
		return "", err
	}
	tagsJSON, err := json.Marshal(input.Tags)
	if err != nil {
		return "", err
	}
	metaJSON, err := json.Marshal(map[string]any{
		"chapter":      input.Chapter,
		"topic":        input.Topic,
		"source":       input.Source,
		"duration":     input.Duration,
		"pages":        input.Pages,
		"storage_type": input.StorageType,
		"views":        0,
		"likes":        0,
	})
	if err != nil {
		return "", err
	}

	_, err = r.DB().Exec(ctx, `
		INSERT INTO public.contents (
			id,
			type,
			owner_teacher_id,
			status,
			title,
			body,
			difficulty,
			concept_ids,
			tags,
			meta,
			created_at,
			updated_at,
			published_at,
			deleted_at
		)
		VALUES ($1, $2::public.contenttype, $3, 'PUBLISHED'::public.contentstatus, $4, $5, $6, '[]'::json, $7::json, $8::json, $9, $9, $9, NULL)`,
		resourceID,
		resourceTypeToDB(input.Type),
		ownerID,
		input.Title,
		input.Body,
		input.Difficulty,
		string(tagsJSON),
		string(metaJSON),
		now,
	)
	if err != nil {
		return "", err
	}

	if input.URL != nil && *input.URL != "" {
		assetID, err := newUUID()
		if err != nil {
			return "", err
		}
		assetMeta, err := json.Marshal(map[string]any{
			"storage_type": input.StorageType,
			"duration":     input.Duration,
			"pages":        input.Pages,
		})
		if err != nil {
			return "", err
		}
		_, err = r.DB().Exec(ctx, `
			INSERT INTO public.content_assets (id, content_id, kind, url, meta, created_at)
			VALUES ($1, $2, $3::public.assetkind, $4, $5::json, $6)`,
			assetID,
			resourceID,
			resourceAssetKind(input.Type),
			*input.URL,
			string(assetMeta),
			now,
		)
		if err != nil {
			return "", err
		}
	}

	return resourceID, nil
}

func (r ResourceRepository) replaceResourceAsset(ctx context.Context, resourceID string, resourceType string, storageType string, duration *string, pages *int, input resourceapp.ResourceUpdate, now time.Time) error {
	_, err := r.DB().Exec(ctx, `
		DELETE FROM public.content_assets
		WHERE content_id = $1`,
		resourceID,
	)
	if err != nil {
		return err
	}
	if input.URL == nil || *input.URL == "" {
		return nil
	}
	assetID, err := newUUID()
	if err != nil {
		return err
	}
	if input.StorageType != nil {
		storageType = *input.StorageType
	}
	assetMeta, err := json.Marshal(map[string]any{
		"storage_type": storageType,
		"duration":     duration,
		"pages":        pages,
	})
	if err != nil {
		return err
	}
	_, err = r.DB().Exec(ctx, `
		INSERT INTO public.content_assets (id, content_id, kind, url, meta, created_at)
		VALUES ($1, $2, $3::public.assetkind, $4, $5::json, $6)`,
		assetID,
		resourceID,
		resourceAssetKind(resourceType),
		*input.URL,
		string(assetMeta),
		now,
	)
	return err
}

func (r ResourceRepository) getResource(ctx context.Context, resourceID string, userID string, onlyPublished bool) (resourceapp.Resource, bool, map[string]any, error) {
	publishedClause := ""
	if onlyPublished {
		publishedClause = "AND c.status = 'PUBLISHED'"
	}
	row := r.DB().QueryRow(ctx, `
		SELECT `+resourceSelectColumns+`
		FROM public.contents c
		LEFT JOIN public.users u ON u.id = c.owner_teacher_id
		LEFT JOIN LATERAL (
			SELECT ca.url
			FROM public.content_assets ca
			WHERE ca.content_id = c.id
			ORDER BY ca.created_at ASC, ca.id ASC
			LIMIT 1
		) asset ON true
		LEFT JOIN public.user_favorites uf ON uf.user_id = $2 AND uf.content_id = c.id
		WHERE c.id = $1 AND c.deleted_at IS NULL
			AND `+resourceContentTypeCondition+` `+publishedClause,
		resourceID,
		userID,
	)
	resource, meta, err := scanResource(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return resourceapp.Resource{}, false, nil, nil
		}
		return resourceapp.Resource{}, false, nil, err
	}
	return resource, true, meta, nil
}

func resourceWhereClause(userID string, filter resourceapp.ListFilter) (string, []any) {
	args := []any{userID}
	conditions := []string{
		"c.status = 'PUBLISHED'",
		"c.deleted_at IS NULL",
		resourceContentTypeCondition,
		"($1::varchar IS NOT NULL)",
	}
	if filter.Type != "" {
		args = append(args, resourceTypeToDB(filter.Type))
		conditions = append(conditions, fmt.Sprintf("c.type = $%d::public.contenttype", len(args)))
	}
	if filter.Chapter != "" {
		args = append(args, filter.Chapter)
		conditions = append(conditions, fmt.Sprintf("c.meta->>'chapter' = $%d", len(args)))
	}
	if filter.Topic != "" {
		args = append(args, filter.Topic)
		conditions = append(conditions, fmt.Sprintf("c.meta->>'topic' = $%d", len(args)))
	}
	if filter.Search != "" {
		args = append(args, filter.Search)
		placeholder := fmt.Sprintf("$%d", len(args))
		conditions = append(conditions, "(c.title ILIKE '%' || "+placeholder+" || '%' OR c.meta->>'topic' ILIKE '%' || "+placeholder+" || '%')")
	}
	if filter.FavoritesOnly {
		conditions = append(conditions, "EXISTS (SELECT 1 FROM public.user_favorites fav WHERE fav.user_id = $1 AND fav.content_id = c.id)")
	}
	return strings.Join(conditions, " AND "), args
}

const resourceContentTypeCondition = "c.type IN ('VIDEO', 'ARTICLE')"

const resourceSelectColumns = `
	c.id,
	c.type::text,
	c.owner_teacher_id,
	c.title,
	c.body,
	c.difficulty,
	c.tags,
	c.meta,
	c.created_at,
	c.updated_at,
	u.display_name,
	asset.url,
	(uf.content_id IS NOT NULL) AS is_favorite`

func scanResourceRow(rows pgx.Rows) (resourceapp.Resource, error) {
	resource, _, err := scanResource(rows)
	return resource, err
}

func scanResource(scanner rowScanner) (resourceapp.Resource, map[string]any, error) {
	var resource resourceapp.Resource
	var dbType string
	var tagsRaw []byte
	var metaRaw []byte
	var ownerName pgtype.Text
	var url pgtype.Text
	if err := scanner.Scan(
		&resource.ID,
		&dbType,
		&resource.OwnerID,
		&resource.Title,
		&resource.Body,
		&resource.Difficulty,
		&tagsRaw,
		&metaRaw,
		&resource.CreatedAt,
		&resource.UpdatedAt,
		&ownerName,
		&url,
		&resource.IsFavorite,
	); err != nil {
		return resourceapp.Resource{}, nil, err
	}
	tags, err := decodeStringSlice(tagsRaw)
	if err != nil {
		return resourceapp.Resource{}, nil, fmt.Errorf("decode resource tags: %w", err)
	}
	meta, err := decodeObjectMap(metaRaw)
	if err != nil {
		return resourceapp.Resource{}, nil, fmt.Errorf("decode resource meta: %w", err)
	}
	resource.OwnerName = textPtr(ownerName)
	resource.URL = textPtr(url)
	resource.Type = resourceTypeFromDB(dbType)
	resource.Tags = tags
	resource.Chapter = metautil.StringPointer(meta, "chapter")
	resource.Topic = metautil.StringPointer(meta, "topic")
	resource.Source = metautil.StringPointer(meta, "source")
	resource.StorageType = metautil.StringPointer(meta, "storage_type")
	resource.Duration = metautil.StringPointer(meta, "duration")
	resource.Pages = metautil.IntPointer(meta, "pages")
	resource.Views = metautil.Int(meta, "views")
	resource.Likes = metautil.Int(meta, "likes")
	return resource, meta, nil
}

func resourceTypeToDB(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "video":
		return "VIDEO"
	case "document", "article":
		return "ARTICLE"
	default:
		return "ARTICLE"
	}
}

func resourceTypeFromDB(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "VIDEO":
		return "video"
	default:
		return "document"
	}
}

func resourceAssetKind(resourceType string) string {
	switch strings.ToLower(strings.TrimSpace(resourceType)) {
	case "video":
		return "VIDEO"
	case "document":
		return "PDF"
	default:
		return "ATTACHMENT"
	}
}

func metaStringDefault(meta map[string]any, key string, fallback string) string {
	value := metautil.StringPointer(meta, key)
	if value == nil {
		return fallback
	}
	return stringutil.NonBlankOr(*value, fallback)
}
