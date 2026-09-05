package postgres

import (
	"context"
	"errors"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	resourceapp "mathstudy/backend/internal/application/resource"
)

const resourceSearchDefaultTenantID = "00000000-0000-4000-8000-000000000001"

// ResolveSearchScope resolves index routing exclusively from current PostgreSQL state.
func (r ResourceRepository) ResolveSearchScope(ctx context.Context, userID string, knowledgeBaseID string, filters resourceapp.SearchFilters) (resourceapp.SearchScope, bool, error) {
	if !validResourceSearchID(userID) || !validResourceSearchID(knowledgeBaseID) || !validResourceSearchFilters(filters) {
		return resourceapp.SearchScope{}, false, errors.New("invalid resource search scope")
	}
	scope := resourceapp.SearchScope{UserID: userID, TenantID: resourceSearchDefaultTenantID, KnowledgeBaseID: knowledgeBaseID, Filters: filters}
	var distance string
	err := r.DB().QueryRow(ctx, `
		SELECT generation.id, generation.generation, generation.model_version_id,
			generation.collection_name, generation.dimension, generation.distance::text
		FROM public.users requester
		JOIN public.tenants tenant ON tenant.id = $2 AND tenant.status = 'active'
		JOIN public.knowledge_bases kb ON kb.id = $3 AND kb.tenant_id = tenant.id AND kb.status = 'active'
		JOIN public.vector_index_generations generation
		  ON generation.knowledge_base_id = kb.id AND generation.tenant_id = tenant.id
		 AND generation.generation = kb.active_generation AND generation.state = 'active'
		JOIN public.embedding_model_versions model
		  ON model.id = generation.model_version_id
		 AND model.logical_name = 'resource_embedding'
		 AND model.dimension = generation.dimension AND model.metric = generation.distance
		WHERE requester.id = $1 AND requester.is_active = true AND requester.status = 'ACTIVE'`,
		userID, scope.TenantID, knowledgeBaseID,
	).Scan(&scope.GenerationID, &scope.Generation, &scope.ModelVersionID, &scope.Collection, &scope.Dimension, &distance)
	if errors.Is(err, pgx.ErrNoRows) {
		return resourceapp.SearchScope{}, false, nil
	}
	if err != nil {
		return resourceapp.SearchScope{}, false, err
	}
	switch distance {
	case "COSINE":
		scope.Distance = resourceapp.VectorDistanceCosine
	case "IP":
		scope.Distance = resourceapp.VectorDistanceDot
	case "L2":
		scope.Distance = resourceapp.VectorDistanceEuclid
	default:
		return resourceapp.SearchScope{}, false, errors.New("invalid resource search distance")
	}
	if !validResourceSearchScope(scope) {
		return resourceapp.SearchScope{}, false, errors.New("invalid resource search index contract")
	}
	rows, err := r.DB().Query(ctx, `SELECT DISTINCT c.id `+resourceSearchFromSQL+`
		WHERE `+resourceSearchVisibleSQL+` ORDER BY c.id LIMIT 1001`, resourceSearchScopeArgs(scope)...)
	if err != nil {
		return resourceapp.SearchScope{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return resourceapp.SearchScope{}, false, err
		}
		scope.ResourceIDs = append(scope.ResourceIDs, id)
	}
	if err := rows.Err(); err != nil {
		return resourceapp.SearchScope{}, false, err
	}
	if len(scope.ResourceIDs) > 1000 {
		scope.ResourceIDs = nil
		scope.ResourceLimitExceeded = true
	}
	return scope, true, nil
}

// SearchLexical returns identifiers only; full text is loaded by final authorization.
func (r ResourceRepository) SearchLexical(ctx context.Context, scope resourceapp.SearchScope, query string, limit int) ([]resourceapp.SearchCandidate, error) {
	if !validResourceSearchScope(scope) || strings.TrimSpace(query) == "" ||
		!utf8.ValidString(query) || strings.ContainsRune(query, '\x00') || utf8.RuneCountInString(query) > 2000 ||
		limit <= 0 || limit > 500 {
		return nil, errors.New("invalid resource lexical search")
	}
	args := resourceSearchScopeArgs(scope)
	pattern := ""
	if strings.IndexFunc(query, func(r rune) bool { return unicode.Is(unicode.Han, r) }) >= 0 {
		pattern = "%" + strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(query) + "%"
	}
	args = append(args, query, limit, pattern)
	rows, err := r.DB().Query(ctx, `
		SELECT chunk.id, c.id, version.id, generation.generation,
			(ts_rank_cd(search_document.value, search_query.value) +
			 CASE WHEN $14::text <> '' AND c.title ILIKE $14 THEN 0.2 ELSE 0 END +
			 CASE WHEN $14::text <> '' AND chunk.content ILIKE $14 THEN 0.05 ELSE 0 END)::double precision
		`+resourceSearchFromSQL+`
		CROSS JOIN LATERAL (
			SELECT setweight(to_tsvector('simple'::regconfig, coalesce(c.title, '')), 'A') ||
				setweight(to_tsvector('simple'::regconfig, chunk.content), 'B') AS value
		) search_document
		CROSS JOIN plainto_tsquery('simple'::regconfig, $12) search_query(value)
		WHERE `+resourceSearchVisibleSQL+`
		  AND (search_document.value @@ search_query.value OR
			($14::text <> '' AND (c.title ILIKE $14 OR chunk.content ILIKE $14)))
		ORDER BY 5 DESC, chunk.id ASC
		LIMIT $13`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]resourceapp.SearchCandidate, 0)
	for rows.Next() {
		var item resourceapp.SearchCandidate
		if err := rows.Scan(&item.ChunkID, &item.ResourceID, &item.DocumentVersionID, &item.Generation, &item.Score); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// AuthorizeSearchChunks repeats current ACL and version checks before returning text.
func (r ResourceRepository) AuthorizeSearchChunks(ctx context.Context, scope resourceapp.SearchScope, candidates []resourceapp.SearchCandidate) ([]resourceapp.AuthorizedSearchChunk, error) {
	if !validResourceSearchScope(scope) || len(candidates) > 500 {
		return nil, errors.New("invalid resource search authorization")
	}
	items := make([]resourceapp.AuthorizedSearchChunk, 0)
	if len(candidates) == 0 {
		return items, nil
	}
	ids := make([]string, 0, len(candidates))
	requested := make(map[string]resourceapp.SearchCandidate, len(candidates))
	for _, candidate := range candidates {
		if !validResourceSearchID(candidate.ChunkID) || !validResourceSearchID(candidate.ResourceID) ||
			!validResourceSearchID(candidate.DocumentVersionID) || candidate.Generation != scope.Generation ||
			math.IsNaN(candidate.Score) || math.IsInf(candidate.Score, 0) {
			return nil, errors.New("invalid resource search candidate")
		}
		if _, exists := requested[candidate.ChunkID]; !exists {
			ids = append(ids, candidate.ChunkID)
			requested[candidate.ChunkID] = candidate
		}
	}
	args := append(resourceSearchScopeArgs(scope), ids)
	rows, err := r.DB().Query(ctx, `
		SELECT chunk.id, c.id, version.id, generation.generation,
			c.title, chunk.content, chunk.content_sha256, chunk.page_no, chunk.section_path
		`+resourceSearchFromSQL+`
		WHERE `+resourceSearchVisibleSQL+`
		  AND chunk.id = ANY($12::varchar[])
		ORDER BY chunk.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item resourceapp.AuthorizedSearchChunk
		var page pgtype.Int4
		var section pgtype.Text
		if err := rows.Scan(&item.Candidate.ChunkID, &item.Candidate.ResourceID, &item.Candidate.DocumentVersionID,
			&item.Candidate.Generation, &item.Title, &item.Content, &item.QuoteHash, &page, &section); err != nil {
			return nil, err
		}
		candidate, exists := requested[item.Candidate.ChunkID]
		if !exists || candidate.ResourceID != item.Candidate.ResourceID ||
			candidate.DocumentVersionID != item.Candidate.DocumentVersionID || candidate.Generation != item.Candidate.Generation {
			continue
		}
		item.Candidate.Score = candidate.Score
		if page.Valid {
			value := int(page.Int32)
			item.Page = &value
		}
		item.SectionPath = textPtr(section)
		items = append(items, item)
	}
	return items, rows.Err()
}

func validResourceSearchID(value string) bool {
	return value != "" && len(value) <= 36 && strings.TrimSpace(value) == value &&
		utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}

func validResourceSearchFilters(filters resourceapp.SearchFilters) bool {
	if filters.Type != "" && filters.Type != "document" && filters.Type != "video" {
		return false
	}
	for _, value := range []string{filters.Chapter, filters.Topic} {
		if !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') || utf8.RuneCountInString(value) > 200 {
			return false
		}
	}
	return true
}

func validResourceSearchScope(scope resourceapp.SearchScope) bool {
	return validResourceSearchID(scope.UserID) && scope.TenantID == resourceSearchDefaultTenantID &&
		validResourceSearchID(scope.KnowledgeBaseID) && validResourceSearchID(scope.ModelVersionID) &&
		scope.Generation > 0 && scope.Dimension > 0 && scope.Dimension <= 65536 &&
		scope.Collection != "" && len(scope.Collection) <= 255 &&
		(scope.Distance == resourceapp.VectorDistanceCosine || scope.Distance == resourceapp.VectorDistanceDot || scope.Distance == resourceapp.VectorDistanceEuclid) &&
		validResourceSearchFilters(scope.Filters)
}

func resourceSearchScopeArgs(scope resourceapp.SearchScope) []any {
	distance := "COSINE"
	switch scope.Distance {
	case resourceapp.VectorDistanceDot:
		distance = "IP"
	case resourceapp.VectorDistanceEuclid:
		distance = "L2"
	}
	contentType := ""
	if scope.Filters.Type != "" {
		contentType = resourceTypeToDB(scope.Filters.Type)
	}
	return []any{scope.UserID, scope.TenantID, scope.KnowledgeBaseID, scope.Generation,
		scope.ModelVersionID, scope.Collection, scope.Dimension, distance, contentType, scope.Filters.Chapter, scope.Filters.Topic}
}

const resourceSearchFromSQL = `
	FROM public.users requester
	JOIN public.tenants tenant ON tenant.id = $2 AND tenant.status = 'active'
	JOIN public.knowledge_bases kb ON kb.id = $3 AND kb.tenant_id = tenant.id
	JOIN public.vector_index_generations generation
	  ON generation.knowledge_base_id = kb.id AND generation.tenant_id = tenant.id
	 AND generation.generation = kb.active_generation
	JOIN public.embedding_model_versions model ON model.id = generation.model_version_id
	JOIN public.resource_memberships membership
	  ON membership.knowledge_base_id = kb.id AND membership.tenant_id = tenant.id
	JOIN public.contents c ON c.id = membership.resource_id AND c.tenant_id = tenant.id
	JOIN public.resource_documents document
	  ON document.resource_id = c.id AND document.tenant_id = tenant.id AND document.knowledge_base_id = kb.id
	JOIN public.document_versions version
	  ON version.id = document.current_version_id AND version.document_id = document.id AND version.tenant_id = tenant.id
	JOIN public.document_chunks chunk ON chunk.document_version_id = version.id AND chunk.tenant_id = tenant.id
	JOIN public.chunk_vector_manifests manifest
	  ON manifest.chunk_id = chunk.id AND manifest.tenant_id = tenant.id AND manifest.generation_id = generation.id
`

// The same predicate governs both coarse recall and final authorization. Unknown
// department deny rules fail closed until authoritative memberships exist.
const resourceSearchVisibleSQL = `
	requester.id = $1 AND requester.is_active = true AND requester.status = 'ACTIVE'
	AND kb.status = 'active' AND generation.state = 'active'
	AND generation.generation = $4 AND generation.model_version_id = $5
	AND generation.collection_name = $6 AND generation.dimension = $7 AND generation.distance::text = $8
	AND model.logical_name = 'resource_embedding'
	AND model.dimension = generation.dimension AND model.metric = generation.distance
	AND membership.status = 'active'
	AND c.status = 'PUBLISHED' AND c.deleted_at IS NULL AND c.type IN ('VIDEO', 'ARTICLE')
	AND ($9::text = '' OR c.type::text = $9)
	AND ($10::text = '' OR c.meta->>'chapter' = $10)
	AND ($11::text = '' OR c.meta->>'topic' = $11)
	AND document.status = 'active' AND document.deleted_at IS NULL
	AND version.process_status = 'succeeded' AND version.index_status = 'ready'
	AND version.published_at IS NOT NULL AND version.deleted_at IS NULL
	AND version.index_generation = generation.generation AND version.model_version_id = generation.model_version_id
	AND chunk.deleted_at IS NULL AND char_length(chunk.content) <= 64000
	AND manifest.state = 'indexed' AND manifest.deleted_at IS NULL
	AND manifest.index_generation = generation.generation AND manifest.model_version_id = generation.model_version_id
	AND manifest.collection_name = generation.collection_name AND manifest.dimension = generation.dimension
	AND NOT EXISTS (
		SELECT 1 FROM public.knowledge_base_acl acl
		WHERE acl.knowledge_base_id = kb.id AND acl.tenant_id = tenant.id
		  AND acl.permission IN ('read', 'manage', 'publish') AND acl.effect = 'deny'
		  AND (acl.valid_from IS NULL OR acl.valid_from <= statement_timestamp() AT TIME ZONE 'UTC')
		  AND (acl.valid_to IS NULL OR acl.valid_to > statement_timestamp() AT TIME ZONE 'UTC')
		  AND (
			(acl.subject_type = 'user' AND acl.subject_id = requester.id)
			OR (acl.subject_type = 'role' AND lower(acl.subject_id) = lower(requester.role::text))
			OR (acl.subject_type = 'tenant' AND acl.subject_id = tenant.id)
			OR (acl.subject_type = 'owner' AND acl.subject_id = requester.id AND c.owner_teacher_id = requester.id)
			OR acl.subject_type = 'department'
		  )
	)
	AND (
		c.owner_teacher_id = requester.id
		OR EXISTS (
			SELECT 1 FROM public.content_acl legacy_acl
			WHERE legacy_acl.content_id = c.id AND legacy_acl.teacher_id = requester.id
			  AND legacy_acl.permission IN ('EDITOR', 'ADMIN')
		)
		OR EXISTS (
			SELECT 1 FROM public.knowledge_base_acl acl
			WHERE acl.knowledge_base_id = kb.id AND acl.tenant_id = tenant.id
			  AND acl.permission IN ('read', 'manage', 'publish') AND acl.effect = 'allow'
			  AND (acl.valid_from IS NULL OR acl.valid_from <= statement_timestamp() AT TIME ZONE 'UTC')
			  AND (acl.valid_to IS NULL OR acl.valid_to > statement_timestamp() AT TIME ZONE 'UTC')
			  AND (
				(acl.subject_type = 'user' AND acl.subject_id = requester.id)
				OR (acl.subject_type = 'role' AND lower(acl.subject_id) = lower(requester.role::text))
				OR (acl.subject_type = 'tenant' AND acl.subject_id = tenant.id)
				OR (acl.subject_type = 'owner' AND acl.subject_id = requester.id AND c.owner_teacher_id = requester.id)
			  )
		)
	)
`
