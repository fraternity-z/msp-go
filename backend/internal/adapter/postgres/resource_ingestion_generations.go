package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	resourceapp "mathstudy/backend/internal/application/resource"
)

const ingestionGenerationSelect = `SELECT g.id,g.tenant_id,g.knowledge_base_id,g.generation,g.collection_name,g.state,
	m.id,m.provider,m.provider_model,m.revision,m.dimension,m.metric::text,m.max_tokens,g.reconcile_cursor,
	coalesce(g.retired_at + interval '7 days', 'epoch'::timestamp)
	FROM public.vector_index_generations g JOIN public.embedding_model_versions m ON m.id=g.model_version_id
	AND m.dimension=g.dimension AND m.metric=g.distance AND m.logical_name='resource_embedding'`

func scanIngestionGeneration(row rowScanner) (resourceapp.IngestionGeneration, error) {
	var item resourceapp.IngestionGeneration
	var distance string
	err := row.Scan(&item.ID, &item.TenantID, &item.KnowledgeBaseID, &item.Number, &item.Collection, &item.State,
		&item.Model.ID, &item.Model.Provider, &item.Model.Model, &item.Model.Revision, &item.Model.Dimension, &distance, &item.Model.MaxTokens, &item.ReconcileCursor, &item.RetainUntil)
	if err != nil {
		return item, err
	}
	switch distance {
	case "COSINE":
		item.Model.Distance = resourceapp.VectorDistanceCosine
	case "IP":
		item.Model.Distance = resourceapp.VectorDistanceDot
	case "L2":
		item.Model.Distance = resourceapp.VectorDistanceEuclid
	default:
		return item, resourceapp.ErrIngestionInvalid
	}
	return item, nil
}

func (r ResourceRepository) ingestionGenerationForModel(ctx context.Context, kbID, modelID string, force bool, now time.Time) (resourceapp.IngestionGeneration, error) {
	var ignored any
	if err := r.DB().QueryRow(ctx, `SELECT pg_advisory_xact_lock(hashtext('resource_embedding'))`).Scan(&ignored); err != nil {
		return resourceapp.IngestionGeneration{}, err
	}
	var active int64
	err := r.DB().QueryRow(ctx, `SELECT kb.active_generation FROM public.knowledge_bases kb JOIN public.tenants t ON t.id=kb.tenant_id
		WHERE kb.id=$1 AND kb.tenant_id=$2 AND kb.status='active' AND t.status='active' FOR UPDATE OF kb`, kbID, resourceSearchDefaultTenantID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return resourceapp.IngestionGeneration{}, resourceapp.ErrAuthorizationDenied
	}
	if err != nil {
		return resourceapp.IngestionGeneration{}, err
	}
	var model resourceapp.EmbeddingModel
	var metric string
	err = r.DB().QueryRow(ctx, `SELECT m.id,m.provider,m.provider_model,m.revision,m.dimension,m.metric::text,m.max_tokens
		FROM public.embedding_model_versions m JOIN public.llm_models source ON source.id=m.llm_model_id
		JOIN public.llm_providers p ON p.id=source.provider_id
		WHERE m.id=$1 AND m.logical_name='resource_embedding' AND m.status='active' AND m.verified_at IS NOT NULL
		AND source.is_active=true AND p.is_active=true AND source.model_id=m.provider_model AND p.code=m.provider`, modelID).
		Scan(&model.ID, &model.Provider, &model.Model, &model.Revision, &model.Dimension, &metric, &model.MaxTokens)
	if errors.Is(err, pgx.ErrNoRows) {
		return resourceapp.IngestionGeneration{}, resourceapp.ErrIngestionModelUnavailable
	}
	if err != nil {
		return resourceapp.IngestionGeneration{}, err
	}
	if !force && active > 0 {
		g, err := scanIngestionGeneration(r.DB().QueryRow(ctx, ingestionGenerationSelect+` WHERE g.knowledge_base_id=$1 AND g.generation=$2 AND g.state='active' AND m.id=$3`, kbID, active, modelID))
		if err == nil {
			return g, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return g, err
		}
	}
	g, err := scanIngestionGeneration(r.DB().QueryRow(ctx, ingestionGenerationSelect+` WHERE g.knowledge_base_id=$1 AND g.state IN ('building','ready') AND m.id=$2 ORDER BY g.generation DESC LIMIT 1`, kbID, modelID))
	if err == nil {
		return g, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return g, err
	}
	// A superseded build cannot publish after an administrator changes the model.
	_, err = r.DB().Exec(ctx, `WITH retired AS (UPDATE public.vector_index_generations SET state='failed',retired_at=$2
		WHERE knowledge_base_id=$1 AND state IN ('pending','building','ready') RETURNING id)
		,cancelled AS (UPDATE public.resource_processing_jobs SET status='cancelled',stage='cancelled',claimed_by=NULL,lease_expires_at=NULL,updated_at=$2,finished_at=$2
		WHERE generation_id IN (SELECT id FROM retired) AND status IN ('pending','running','failed','dead') RETURNING outbox_event_id)
		UPDATE public.outbox_events SET processed_at=$2,lease_owner=NULL,lease_expires_at=NULL WHERE id IN (SELECT outbox_event_id FROM cancelled)`, kbID, now)
	if err != nil {
		return g, err
	}
	id, err := newUUID()
	if err != nil {
		return g, err
	}
	collection := "resource_" + strings.ReplaceAll(kbID, "-", "") + "_" + strings.ReplaceAll(id, "-", "")
	_, err = r.DB().Exec(ctx, `INSERT INTO public.vector_index_generations
		(id,tenant_id,knowledge_base_id,model_version_id,generation,collection_name,dimension,distance,state,created_at)
		SELECT $1::varchar,$2::varchar,$3::varchar,$4::varchar,coalesce(max(generation),0)+1,$5::varchar,$6::integer,$7::public.distancemetric,'building',$8::timestamp
		FROM public.vector_index_generations WHERE knowledge_base_id=$3`, id, resourceSearchDefaultTenantID, kbID, modelID, collection, model.Dimension, metric, now)
	if err != nil {
		return g, err
	}
	g, err = scanIngestionGeneration(r.DB().QueryRow(ctx, ingestionGenerationSelect+` WHERE g.id=$1`, id))
	if err != nil {
		return g, err
	}
	if err := r.enqueueGenerationVersions(ctx, g, now); err != nil {
		return g, err
	}
	return g, nil
}

func (r ResourceRepository) enqueueGenerationVersions(ctx context.Context, g resourceapp.IngestionGeneration, now time.Time) error {
	rows, err := r.DB().Query(ctx, `SELECT c.id,v.id,c.status::text FROM public.resource_documents d JOIN public.contents c ON c.id=d.resource_id
		JOIN public.document_versions v ON v.id=d.current_version_id AND v.document_id=d.id
		JOIN public.resource_memberships rm ON rm.resource_id=c.id AND rm.knowledge_base_id=d.knowledge_base_id
		WHERE d.knowledge_base_id=$1 AND d.tenant_id=$2 AND c.tenant_id=$2 AND v.tenant_id=$2 AND rm.tenant_id=$2
		AND d.status='active' AND d.deleted_at IS NULL AND c.deleted_at IS NULL AND c.status IN ('DRAFT','PUBLISHED')
		AND v.deleted_at IS NULL AND rm.status='active'
		AND (v.chunk_count=0 OR v.chunk_count<>(SELECT count(*) FROM public.document_chunks ch JOIN public.chunk_vector_manifests m ON m.chunk_id=ch.id
			WHERE ch.document_version_id=v.id AND ch.deleted_at IS NULL AND m.generation_id=$3 AND m.state='indexed' AND m.deleted_at IS NULL))
		ORDER BY c.id`, g.KnowledgeBaseID, g.TenantID, g.ID)
	if err != nil {
		return err
	}
	type pair struct{ resource, version, status string }
	var versions []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.resource, &p.version, &p.status); err != nil {
			rows.Close()
			return err
		}
		versions = append(versions, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, p := range versions {
		kind := resourceapp.IngestionJobRebuild
		if p.status == "DRAFT" {
			kind = resourceapp.IngestionJobIngest
		}
		if err := r.enqueueIngestion(ctx, kind, p.resource, p.version, g, now); err != nil {
			return err
		}
	}
	return nil
}

func (r ResourceRepository) BeginIngestionRebuild(ctx context.Context, kbID, modelID string, now time.Time) (resourceapp.IngestionGeneration, error) {
	if !validResourceSearchID(kbID) || !validResourceSearchID(modelID) {
		return resourceapp.IngestionGeneration{}, resourceapp.ErrIngestionInvalid
	}
	var g resourceapp.IngestionGeneration
	err := r.withIngestionTx(ctx, func(tx ResourceRepository) error {
		var err error
		g, err = tx.ingestionGenerationForModel(ctx, kbID, modelID, true, now)
		return err
	})
	return g, err
}

func (r ResourceRepository) ListIngestionGenerations(ctx context.Context, afterID string, limit int) ([]resourceapp.IngestionGeneration, error) {
	if limit < 1 || limit > 1000 || len(afterID) > 36 {
		return nil, resourceapp.ErrIngestionInvalid
	}
	rows, err := r.DB().Query(ctx, ingestionGenerationSelect+` WHERE g.tenant_id=$1 AND g.id>$2 ORDER BY g.id LIMIT $3`, resourceSearchDefaultTenantID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]resourceapp.IngestionGeneration, 0)
	for rows.Next() {
		item, err := scanIngestionGeneration(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r ResourceRepository) ListIngestionManifests(ctx context.Context, generationID, afterID string, limit int) ([]resourceapp.IngestionManifest, error) {
	if !validResourceSearchID(generationID) || len(afterID) > 36 || limit < 1 || limit > 1000 {
		return nil, resourceapp.ErrIngestionInvalid
	}
	return r.queryIngestionManifests(ctx, generationID, afterID, nil, limit)
}

func (r ResourceRepository) GetIngestionManifests(ctx context.Context, generationID string, ids []string) ([]resourceapp.IngestionManifest, error) {
	if !validResourceSearchID(generationID) || len(ids) > 1000 {
		return nil, resourceapp.ErrIngestionInvalid
	}
	valid := make([]string, 0, len(ids))
	for _, id := range ids {
		if validResourceSearchID(id) {
			valid = append(valid, id)
		}
	}
	ids = valid
	if len(ids) == 0 {
		return []resourceapp.IngestionManifest{}, nil
	}
	return r.queryIngestionManifests(ctx, generationID, "", ids, 1000)
}

func (r ResourceRepository) queryIngestionManifests(ctx context.Context, generationID, afterID string, ids []string, limit int) ([]resourceapp.IngestionManifest, error) {
	rows, err := r.DB().Query(ctx, `SELECT m.id,ch.id,c.id,v.id,g.id,m.embedding_sha256,ch.content_sha256,m.state,
			((g.state IN ('active','building','ready') OR (g.state='retired' AND g.retired_at + interval '7 days' > statement_timestamp() AT TIME ZONE 'UTC'))
			AND c.status IN ('DRAFT','PUBLISHED') AND c.deleted_at IS NULL
		AND d.current_version_id=v.id AND d.status='active' AND d.deleted_at IS NULL AND v.deleted_at IS NULL
		AND ch.deleted_at IS NULL AND rm.status='active' AND m.state<>'deleted' AND m.deleted_at IS NULL) AS desired
		FROM public.chunk_vector_manifests m JOIN public.vector_index_generations g ON g.id=m.generation_id
		JOIN public.document_chunks ch ON ch.id=m.chunk_id AND ch.tenant_id=m.tenant_id
		JOIN public.document_versions v ON v.id=ch.document_version_id AND v.tenant_id=m.tenant_id
		JOIN public.resource_documents d ON d.id=v.document_id AND d.tenant_id=m.tenant_id
		JOIN public.contents c ON c.id=d.resource_id AND c.tenant_id=m.tenant_id
		JOIN public.resource_memberships rm ON rm.resource_id=c.id AND rm.knowledge_base_id=g.knowledge_base_id AND rm.tenant_id=m.tenant_id
			WHERE m.generation_id=$1 AND m.id>$2 AND m.tenant_id=$3 AND ($5::varchar[] IS NULL OR m.id=ANY($5)) ORDER BY m.id LIMIT $4`, generationID, afterID, resourceSearchDefaultTenantID, limit, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]resourceapp.IngestionManifest, 0)
	for rows.Next() {
		var item resourceapp.IngestionManifest
		if err := rows.Scan(&item.ID, &item.ChunkID, &item.ResourceID, &item.DocumentVersionID, &item.GenerationID, &item.EmbeddingSHA256, &item.ContentSHA256, &item.State, &item.Desired); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// IngestionQueueStats contains counts only, suitable for operational output.
func (r ResourceRepository) IngestionQueueStats(ctx context.Context) (map[string]int64, error) {
	var queued, running, dead, oldest int64
	err := r.DB().QueryRow(ctx, `SELECT count(*) FILTER(WHERE status='pending'),count(*) FILTER(WHERE status='running'),
		count(*) FILTER(WHERE status IN ('dead','failed')),
		coalesce(greatest(0,extract(epoch FROM (statement_timestamp() AT TIME ZONE 'UTC' - min(created_at) FILTER(WHERE status='pending'))))::bigint,0)
		FROM (`+ingestionJobStateSQL+`) jobs WHERE tenant_id=$1 AND generation_id IS NOT NULL`, resourceSearchDefaultTenantID).Scan(&queued, &running, &dead, &oldest)
	return map[string]int64{"queued": queued, "running": running, "dead": dead, "oldest_wait_seconds": oldest}, err
}

func (r ResourceRepository) ScheduleIngestionRepair(ctx context.Context, generationID, versionID string, now time.Time) (bool, error) {
	if !validResourceSearchID(generationID) || !validResourceSearchID(versionID) {
		return false, resourceapp.ErrIngestionInvalid
	}
	created := false
	err := r.withIngestionTx(ctx, func(tx ResourceRepository) error {
		g, err := scanIngestionGeneration(tx.DB().QueryRow(ctx, ingestionGenerationSelect+` WHERE g.id=$1 AND g.tenant_id=$2 AND g.state IN ('active','building','ready') AND m.status='active'`, generationID, resourceSearchDefaultTenantID))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var resourceID string
		err = tx.DB().QueryRow(ctx, `SELECT c.id FROM public.document_versions v JOIN public.resource_documents d ON d.current_version_id=v.id
			JOIN public.contents c ON c.id=d.resource_id JOIN public.resource_memberships rm ON rm.resource_id=c.id AND rm.knowledge_base_id=d.knowledge_base_id
			WHERE v.id=$1 AND d.knowledge_base_id=$2 AND d.tenant_id=$3 AND v.tenant_id=$3 AND c.tenant_id=$3 AND rm.tenant_id=$3
			AND c.status='PUBLISHED' AND c.deleted_at IS NULL AND d.status='active' AND d.deleted_at IS NULL AND v.deleted_at IS NULL AND rm.status='active'`, versionID, g.KnowledgeBaseID, g.TenantID).Scan(&resourceID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var running bool
		if err := tx.DB().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.resource_processing_jobs WHERE generation_id=$1 AND document_version_id=$2 AND status IN ('pending','running'))`, generationID, versionID).Scan(&running); err != nil {
			return err
		}
		if running {
			return nil
		}
		if err := tx.enqueueIngestion(ctx, resourceapp.IngestionJobRebuild, resourceID, versionID, g, now); err != nil {
			return err
		}
		created = true
		return nil
	})
	return created, err
}

func (r ResourceRepository) SaveIngestionReconcileCursor(ctx context.Context, generationID, cursor string) error {
	if !validResourceSearchID(generationID) || len(cursor) > 128 || !validIngestionText(cursor, 128, true) {
		return resourceapp.ErrIngestionInvalid
	}
	_, err := r.DB().Exec(ctx, `UPDATE public.vector_index_generations SET reconcile_cursor=$2 WHERE id=$1 AND tenant_id=$3`, generationID, cursor, resourceSearchDefaultTenantID)
	return err
}

// A build switches all publication contracts together, after every current document is complete.
func (r ResourceRepository) activateIngestionGeneration(ctx context.Context, g resourceapp.IngestionGeneration, now time.Time) error {
	if g.State != "building" && g.State != "ready" {
		return nil
	}
	var ignored any
	if err := r.DB().QueryRow(ctx, `SELECT pg_advisory_xact_lock(hashtext('resource_embedding'))`).Scan(&ignored); err != nil {
		return err
	}
	var current bool
	if err := r.DB().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.embedding_model_versions WHERE id=$1 AND logical_name='resource_embedding' AND status='active')`, g.Model.ID).Scan(&current); err != nil {
		return err
	}
	if !current {
		return nil
	}
	var pending bool
	if err := r.DB().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM (`+ingestionJobStateSQL+`) jobs
		JOIN public.contents c ON c.id=jobs.resource_id WHERE jobs.generation_id=$1 AND jobs.job_type<>'purge'
		AND jobs.status NOT IN ('succeeded','cancelled') AND c.status IN ('DRAFT','PUBLISHED') AND c.deleted_at IS NULL)`, g.ID).Scan(&pending); err != nil {
		return err
	}
	if pending {
		return nil
	}
	var incomplete bool
	if err := r.DB().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.resource_documents d JOIN public.contents c ON c.id=d.resource_id
		JOIN public.document_versions v ON v.id=d.current_version_id JOIN public.resource_memberships rm ON rm.resource_id=c.id AND rm.knowledge_base_id=d.knowledge_base_id
			WHERE d.knowledge_base_id=$1 AND (c.status='PUBLISHED' OR (c.status='DRAFT' AND EXISTS(
				SELECT 1 FROM (`+ingestionJobStateSQL+`) j WHERE j.generation_id=$2 AND j.document_version_id=v.id AND j.status='succeeded' AND j.job_type='ingest')))
			AND c.deleted_at IS NULL AND d.status='active' AND d.deleted_at IS NULL AND rm.status='active' AND v.deleted_at IS NULL
		AND (v.chunk_count=0 OR v.chunk_count<>(SELECT count(*) FROM public.document_chunks ch JOIN public.chunk_vector_manifests m ON m.chunk_id=ch.id
			WHERE ch.document_version_id=v.id AND ch.deleted_at IS NULL AND m.generation_id=$2 AND m.state='indexed' AND m.deleted_at IS NULL)))`, g.KnowledgeBaseID, g.ID).Scan(&incomplete); err != nil {
		return err
	}
	if incomplete {
		return r.enqueueGenerationVersions(ctx, g, now)
	}
	_, err := r.DB().Exec(ctx, `UPDATE public.document_versions v SET process_status='succeeded',index_status='ready',
		index_generation=$2,model_version_id=$3,published_at=coalesce(v.published_at,$4),error_code=NULL,error_message=NULL
		FROM (`+ingestionJobStateSQL+`) j JOIN public.contents c ON c.id=j.resource_id
		WHERE j.generation_id=$1 AND j.document_version_id=v.id AND j.status='succeeded' AND j.job_type IN ('ingest','rebuild')
		AND c.deleted_at IS NULL AND c.status IN ('DRAFT','PUBLISHED') AND v.deleted_at IS NULL`, g.ID, g.Number, g.Model.ID, now)
	if err != nil {
		return err
	}
	_, err = r.DB().Exec(ctx, `UPDATE public.contents c SET status='PUBLISHED',published_at=coalesce(c.published_at,$2),updated_at=$2
		FROM (`+ingestionJobStateSQL+`) j WHERE j.generation_id=$1 AND j.resource_id=c.id AND j.status='succeeded'
		AND j.job_type='ingest' AND c.deleted_at IS NULL AND c.status='DRAFT'`, g.ID, now)
	if err != nil {
		return err
	}
	_, err = r.DB().Exec(ctx, `UPDATE public.vector_index_generations SET state='retired',retired_at=$2 WHERE knowledge_base_id=$1 AND state='active' AND id<>$3`, g.KnowledgeBaseID, now, g.ID)
	if err != nil {
		return err
	}
	_, err = r.DB().Exec(ctx, `UPDATE public.document_versions v SET index_generation=$2,model_version_id=$3,index_status='ready'
		FROM public.resource_documents d JOIN public.contents c ON c.id=d.resource_id WHERE v.id=d.current_version_id AND d.knowledge_base_id=$1
		AND c.status='PUBLISHED' AND c.deleted_at IS NULL AND d.status='active' AND d.deleted_at IS NULL AND v.deleted_at IS NULL`, g.KnowledgeBaseID, g.Number, g.Model.ID)
	if err != nil {
		return err
	}
	_, err = r.DB().Exec(ctx, `UPDATE public.vector_index_generations SET state='active',activated_at=$2 WHERE id=$1 AND state IN ('building','ready')`, g.ID, now)
	if err != nil {
		return err
	}
	_, err = r.DB().Exec(ctx, `UPDATE public.knowledge_bases SET active_generation=$2,updated_at=$3 WHERE id=$1 AND tenant_id=$4`, g.KnowledgeBaseID, g.Number, now, g.TenantID)
	return err
}
