package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	resourceapp "mathstudy/backend/internal/application/resource"
)

// ResolveVectorManifests maps provider point IDs to current PostgreSQL identities.
func (r ResourceRepository) ResolveVectorManifests(ctx context.Context, scope resourceapp.SearchScope, pointIDs []string) ([]resourceapp.VectorManifestIdentity, error) {
	if !validResourceSearchScope(scope) || !validResourceSearchID(scope.GenerationID) || len(pointIDs) > 100 {
		return nil, errors.New("invalid vector manifest lookup")
	}
	items := make([]resourceapp.VectorManifestIdentity, 0, len(pointIDs))
	if len(pointIDs) == 0 {
		return items, nil
	}
	for _, id := range pointIDs {
		if !validResourceSearchID(id) {
			return nil, errors.New("invalid vector point identity")
		}
	}
	args := append(resourceSearchScopeArgs(scope), pointIDs, scope.GenerationID)
	rows, err := r.DB().Query(ctx, `
		SELECT manifest.id, chunk.id, c.id, version.id, generation.generation, generation.model_version_id
		`+resourceSearchFromSQL+` WHERE `+resourceSearchVisibleSQL+`
		AND manifest.id = ANY($12::varchar[]) AND generation.id = $13 ORDER BY manifest.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item resourceapp.VectorManifestIdentity
		if err := rows.Scan(&item.PointID, &item.ChunkID, &item.ResourceID, &item.DocumentVersionID, &item.Generation, &item.ModelVersionID); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// SearchNeighbors returns only identifiers. The caller must authorize them again
// before reading text, including when a seed has been deleted during expansion.
func (r ResourceRepository) SearchNeighbors(ctx context.Context, scope resourceapp.SearchScope, seeds []resourceapp.SearchCandidate, limit int) ([]resourceapp.SearchCandidate, error) {
	if !validResourceSearchScope(scope) || len(seeds) > 20 || limit < 1 || limit > 2 {
		return nil, errors.New("invalid resource neighbor lookup")
	}
	items := make([]resourceapp.SearchCandidate, 0)
	if len(seeds) == 0 {
		return items, nil
	}
	ids := make([]string, len(seeds))
	for i, seed := range seeds {
		if !validResourceSearchID(seed.ChunkID) || seed.Generation != scope.Generation {
			return nil, errors.New("invalid neighbor seed")
		}
		ids[i] = seed.ChunkID
	}
	args := append(resourceSearchScopeArgs(scope), ids, limit)
	rows, err := r.DB().Query(ctx, `
		SELECT DISTINCT candidate.id, candidate.resource_id, candidate.version_id, candidate.generation
		FROM (
			SELECT seed.id FROM public.document_chunks seed WHERE seed.id = ANY($12::varchar[])
		) selected
		CROSS JOIN LATERAL (
			SELECT chunk.id, c.id AS resource_id, version.id AS version_id, generation.generation
			`+resourceSearchFromSQL+`
			JOIN public.document_chunks seed ON seed.id = selected.id AND seed.document_version_id = version.id
			 AND seed.tenant_id = tenant.id AND seed.deleted_at IS NULL
			WHERE `+resourceSearchVisibleSQL+`
			 AND chunk.id <> seed.id
			 AND (chunk.ordinal IN (seed.ordinal - 1, seed.ordinal + 1) OR chunk.id = seed.parent_chunk_id)
			ORDER BY CASE WHEN chunk.id = seed.parent_chunk_id THEN 0 ELSE 1 END,
			 abs(chunk.ordinal - seed.ordinal), chunk.ordinal, chunk.id LIMIT $13
		) candidate
		ORDER BY candidate.version_id, candidate.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item resourceapp.SearchCandidate
		if err := rows.Scan(&item.ChunkID, &item.ResourceID, &item.DocumentVersionID, &item.Generation); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// GetSearchCitation never treats a historical citation as an access token.
func (r ResourceRepository) GetSearchCitation(ctx context.Context, scope resourceapp.SearchScope, request resourceapp.CitationRequest) (resourceapp.AuthorizedSearchChunk, bool, error) {
	if !validResourceSearchScope(scope) || !validResourceSearchID(request.ChunkID) ||
		!validResourceSearchID(request.DocumentVersionID) || request.Generation != scope.Generation {
		return resourceapp.AuthorizedSearchChunk{}, false, nil
	}
	args := append(resourceSearchScopeArgs(scope), request.ChunkID, request.DocumentVersionID)
	var resourceID string
	err := r.DB().QueryRow(ctx, `SELECT c.id `+resourceSearchFromSQL+`
		WHERE `+resourceSearchVisibleSQL+` AND chunk.id = $12 AND version.id = $13`, args...).Scan(&resourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return resourceapp.AuthorizedSearchChunk{}, false, nil
	}
	if err != nil {
		return resourceapp.AuthorizedSearchChunk{}, false, err
	}
	items, err := r.AuthorizeSearchChunks(ctx, scope, []resourceapp.SearchCandidate{{
		ChunkID: request.ChunkID, ResourceID: resourceID, DocumentVersionID: request.DocumentVersionID, Generation: request.Generation,
	}})
	if err != nil {
		return resourceapp.AuthorizedSearchChunk{}, false, err
	}
	if len(items) != 1 {
		return resourceapp.AuthorizedSearchChunk{}, false, nil
	}
	return items[0], true, nil
}
