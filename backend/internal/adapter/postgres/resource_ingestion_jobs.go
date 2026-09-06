package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	resourceapp "mathstudy/backend/internal/application/resource"
)

func (r ResourceRepository) enqueueIngestion(ctx context.Context, kind, resourceID, versionID string, g resourceapp.IngestionGeneration, now time.Time) error {
	key := kind + ":" + versionID + ":" + g.ID
	jobID := ingestionID("job", key)
	eventID := ingestionID("event", key)
	var running bool
	if err := r.DB().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.resource_processing_jobs WHERE id=$1 AND status IN ('pending','running'))`, jobID).Scan(&running); err != nil {
		return err
	}
	if running {
		return nil
	}
	payload, _ := json.Marshal(map[string]string{"job_id": jobID, "resource_id": resourceID, "document_version_id": versionID, "generation_id": g.ID})
	_, err := r.DB().Exec(ctx, `INSERT INTO public.outbox_events (id,type,payload,created_at,retry_count,tenant_id,aggregate_type,aggregate_id,idempotency_key,available_at)
		VALUES ($1,'EMBEDDING_REQUIRED',$2::json,$3,0,$4,'resource_ingestion',$5,$6,$3)
		ON CONFLICT(id) DO UPDATE SET processed_at=NULL,dead_at=NULL,available_at=$3,error_code=NULL,last_error=NULL,lease_owner=NULL,lease_expires_at=NULL,retry_count=0`, eventID, string(payload), now, g.TenantID, resourceID, key)
	if err != nil {
		return err
	}
	_, err = r.DB().Exec(ctx, `INSERT INTO public.resource_processing_jobs
		(id,tenant_id,job_type,resource_id,document_version_id,idempotency_key,generation_id,outbox_event_id,created_at,updated_at,available_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9,$9)
		ON CONFLICT(id) DO UPDATE SET status='pending',stage='queued',attempt_count=0,available_at=$9,claimed_by=NULL,
		lease_expires_at=NULL,heartbeat_at=NULL,last_error_code=NULL,last_error_message=NULL,finished_at=NULL,updated_at=$9
		WHERE public.resource_processing_jobs.status NOT IN ('pending','running')`, jobID, g.TenantID, kind, resourceID, versionID, key, g.ID, eventID, now)
	return err
}

func validIngestionLease(lease resourceapp.IngestionLease) bool {
	return validResourceSearchID(lease.JobID) && validIngestionText(lease.Owner, 100, false) && lease.Attempt > 0
}

func (r ResourceRepository) ClaimIngestionJob(ctx context.Context, owner string, now, until time.Time) (resourceapp.IngestionWork, bool, error) {
	if !validIngestionText(owner, 100, false) || !until.After(now) || until.Sub(now) > 30*time.Minute {
		return resourceapp.IngestionWork{}, false, resourceapp.ErrIngestionInvalid
	}
	var work resourceapp.IngestionWork
	found := false
	err := r.withIngestionTx(ctx, func(tx ResourceRepository) error {
		// Exhausted crashed workers must not remain running indefinitely.
		_, err := tx.DB().Exec(ctx, `WITH dead AS (UPDATE public.resource_processing_jobs SET status='dead',stage='dead',last_error_code='lease_expired',
			claimed_by=NULL,lease_expires_at=NULL,finished_at=$1,updated_at=$1
			WHERE tenant_id=$2 AND generation_id IS NOT NULL AND attempt_count>=max_attempts
			AND ((status='running' AND lease_expires_at<=$1) OR status='pending') RETURNING outbox_event_id)
			UPDATE public.outbox_events SET dead_at=$1,error_code='lease_expired',lease_owner=NULL,lease_expires_at=NULL WHERE id IN(SELECT outbox_event_id FROM dead)`, now, resourceSearchDefaultTenantID)
		if err != nil {
			return err
		}
		var id string
		err = tx.DB().QueryRow(ctx, `WITH candidate AS (
			SELECT j.id FROM public.resource_processing_jobs j JOIN public.vector_index_generations g ON g.id=j.generation_id
			WHERE j.tenant_id=$1 AND j.job_type IN ('ingest','rebuild','purge') AND j.attempt_count<j.max_attempts
			AND ((j.status='pending' AND j.available_at<=$2) OR (j.status='running' AND j.lease_expires_at<=$2))
			AND (j.job_type='purge' OR g.state IN ('active','building','ready'))
			ORDER BY j.priority DESC,j.available_at,j.created_at,j.id FOR UPDATE OF j SKIP LOCKED LIMIT 1)
			UPDATE public.resource_processing_jobs j SET status='running',stage=CASE WHEN job_type='purge' THEN 'purging' WHEN job_type='rebuild' THEN 'indexing' ELSE 'parsing' END,
			attempt_count=attempt_count+1,claimed_by=$3,lease_expires_at=$4,heartbeat_at=$2,updated_at=$2
			FROM candidate WHERE j.id=candidate.id RETURNING j.id`, resourceSearchDefaultTenantID, now, owner, until).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		work, err = tx.loadIngestionWork(ctx, id)
		if err != nil {
			return err
		}
		_, err = tx.DB().Exec(ctx, `UPDATE public.outbox_events SET lease_owner=$2,lease_expires_at=$3,heartbeat_at=$4,retry_count=$5
			WHERE id=(SELECT outbox_event_id FROM public.resource_processing_jobs WHERE id=$1)`, id, owner, until, now, work.Job.Attempt)
		if err != nil {
			return err
		}
		if work.Job.Type == resourceapp.IngestionJobIngest {
			_, err = tx.DB().Exec(ctx, `UPDATE public.document_versions SET process_status='processing',index_status='building'
				WHERE id=$1 AND published_at IS NULL AND deleted_at IS NULL`, work.Job.DocumentVersionID)
			if err != nil {
				return err
			}
		}
		found = true
		return nil
	})
	return work, found, err
}

func (r ResourceRepository) loadIngestionWork(ctx context.Context, id string) (resourceapp.IngestionWork, error) {
	var work resourceapp.IngestionWork
	var generationID string
	err := r.DB().QueryRow(ctx, `SELECT j.id,j.job_type,j.tenant_id,j.resource_id,j.document_version_id,j.idempotency_key,j.attempt_count,j.max_attempts,
		j.available_at,j.lease_expires_at,coalesce(j.claimed_by,''),j.generation_id,
		coalesce(v.source_metadata->>'uri',d.object_uri,d.source_uri,''),coalesce(v.source_metadata->>'storage_key',''),
		coalesce(d.filename,''),d.mime_type,d.byte_size,d.checksum_sha256
		FROM public.resource_processing_jobs j JOIN public.document_versions v ON v.id=j.document_version_id AND v.tenant_id=j.tenant_id
		JOIN public.resource_documents d ON d.id=v.document_id AND d.resource_id=j.resource_id AND d.tenant_id=j.tenant_id
		WHERE j.id=$1 AND j.tenant_id=$2`, id, resourceSearchDefaultTenantID).
		Scan(&work.Job.ID, &work.Job.Type, &work.Job.TenantID, &work.Job.ResourceID, &work.Job.DocumentVersionID, &work.Job.IdempotencyKey, &work.Job.Attempt, &work.Job.MaxAttempts,
			&work.Job.AvailableAt, &work.Job.LeaseExpiresAt, &work.Lease.Owner, &generationID, &work.Source.URI, &work.Source.StorageKey,
			&work.Metadata.Filename, &work.Metadata.MIMEType, &work.Metadata.ByteSize, &work.Metadata.Checksum)
	if err != nil {
		return work, err
	}
	work.Lease.JobID, work.Lease.Attempt = work.Job.ID, work.Job.Attempt
	work.Generation, err = scanIngestionGeneration(r.DB().QueryRow(ctx, ingestionGenerationSelect+` WHERE g.id=$1 AND g.tenant_id=$2`, generationID, work.Job.TenantID))
	if err != nil {
		return work, err
	}
	work.Chunks, err = r.loadIngestionChunks(ctx, work.Job.DocumentVersionID, work.Generation.ID)
	return work, err
}

func (r ResourceRepository) fencedIngestionWork(ctx context.Context, lease resourceapp.IngestionLease, now time.Time) (resourceapp.IngestionWork, error) {
	if !validIngestionLease(lease) {
		return resourceapp.IngestionWork{}, resourceapp.ErrIngestionInvalid
	}
	var id string
	err := r.DB().QueryRow(ctx, `SELECT j.id FROM public.resource_processing_jobs j
		JOIN public.contents c ON c.id=j.resource_id AND c.tenant_id=j.tenant_id
		JOIN public.document_versions v ON v.id=j.document_version_id AND v.tenant_id=j.tenant_id
		JOIN public.resource_documents d ON d.id=v.document_id AND d.tenant_id=j.tenant_id AND d.resource_id=c.id
		JOIN public.knowledge_bases kb ON kb.id=d.knowledge_base_id AND kb.tenant_id=j.tenant_id
		JOIN public.tenants tenant ON tenant.id=kb.tenant_id
		JOIN public.vector_index_generations g ON g.id=j.generation_id AND g.knowledge_base_id=kb.id AND g.tenant_id=j.tenant_id
		JOIN public.resource_memberships rm ON rm.resource_id=c.id AND rm.knowledge_base_id=kb.id AND rm.tenant_id=j.tenant_id
		WHERE j.id=$1 AND j.claimed_by=$2 AND j.attempt_count=$3 AND j.status='running' AND j.lease_expires_at>$4
		AND j.tenant_id=$5 AND (j.job_type='purge' OR (c.deleted_at IS NULL AND c.status IN ('DRAFT','PUBLISHED')
		AND d.status='active' AND d.deleted_at IS NULL AND v.deleted_at IS NULL AND d.current_version_id=v.id
		AND rm.status='active' AND kb.status='active' AND tenant.status='active' AND g.state IN ('active','building','ready')
		AND EXISTS(SELECT 1 FROM public.users owner WHERE owner.id=c.owner_teacher_id AND owner.is_active=true AND owner.status='ACTIVE' AND owner.role IN ('TEACHER','ADMIN'))))
		FOR UPDATE OF j,c,d,v,g,kb`, lease.JobID, lease.Owner, lease.Attempt, now, resourceSearchDefaultTenantID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return resourceapp.IngestionWork{}, resourceapp.ErrIngestionLeaseLost
	}
	if err != nil {
		return resourceapp.IngestionWork{}, err
	}
	return r.loadIngestionWork(ctx, id)
}

func (r ResourceRepository) HeartbeatIngestionJob(ctx context.Context, lease resourceapp.IngestionLease, now, until time.Time) (bool, error) {
	if !validIngestionLease(lease) || !until.After(now) || until.Sub(now) > 30*time.Minute {
		return false, resourceapp.ErrIngestionInvalid
	}
	ok := false
	err := r.withIngestionTx(ctx, func(tx ResourceRepository) error {
		_, err := tx.fencedIngestionWork(ctx, lease, now)
		if errors.Is(err, resourceapp.ErrIngestionLeaseLost) {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = tx.DB().Exec(ctx, `WITH renewed AS (UPDATE public.resource_processing_jobs SET lease_expires_at=$2,heartbeat_at=$3,updated_at=$3 WHERE id=$1 RETURNING outbox_event_id)
			UPDATE public.outbox_events SET lease_expires_at=$2,heartbeat_at=$3 WHERE id IN(SELECT outbox_event_id FROM renewed)`, lease.JobID, until, now)
		ok = err == nil
		return err
	})
	return ok, err
}

func (r ResourceRepository) FailIngestionJob(ctx context.Context, lease resourceapp.IngestionLease, code string, retryable bool, nextAt, now time.Time) (bool, error) {
	if !validIngestionLease(lease) || !validIngestionErrorCode(code) || nextAt.Before(now) {
		return false, resourceapp.ErrIngestionInvalid
	}
	ok := false
	err := r.withIngestionTx(ctx, func(tx ResourceRepository) error {
		work, err := tx.fencedIngestionWork(ctx, lease, now)
		if errors.Is(err, resourceapp.ErrIngestionLeaseLost) {
			return nil
		}
		if err != nil {
			return err
		}
		status := "failed"
		stage := "failed"
		if retryable {
			status = "pending"
			stage = "queued"
			if work.Job.Attempt >= work.Job.MaxAttempts {
				status = "dead"
				stage = "dead"
			}
		}
		_, err = tx.DB().Exec(ctx, `UPDATE public.resource_processing_jobs SET status=$2::varchar,stage=$3,available_at=$4,claimed_by=NULL,lease_expires_at=NULL,
			last_error_code=$5,last_error_message=NULL,updated_at=$6::timestamp,finished_at=CASE WHEN $2='pending' THEN NULL ELSE $6::timestamp END WHERE id=$1`, lease.JobID, status, stage, nextAt, code, now)
		if err != nil {
			return err
		}
		_, err = tx.DB().Exec(ctx, `UPDATE public.outbox_events SET available_at=$2,lease_owner=NULL,lease_expires_at=NULL,error_code=$3,last_error=NULL,
			dead_at=CASE WHEN $4='pending' THEN NULL ELSE $5::timestamp END WHERE id=(SELECT outbox_event_id FROM public.resource_processing_jobs WHERE id=$1)`, lease.JobID, nextAt, code, status, now)
		if err != nil {
			return err
		}
		if work.Job.Type == resourceapp.IngestionJobIngest {
			process, index := "failed", "failed"
			if status == "pending" {
				process, index = "queued", "pending"
			}
			_, err = tx.DB().Exec(ctx, `UPDATE public.document_versions SET process_status=$2,index_status=$3,error_code=$4,error_message=NULL WHERE id=$1 AND published_at IS NULL`, work.Job.DocumentVersionID, process, index, code)
			if err != nil {
				return err
			}
		}
		ok = true
		return nil
	})
	return ok, err
}

func validIngestionErrorCode(value string) bool {
	if value == "" || len(value) > 100 {
		return false
	}
	return strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_')
	}) < 0
}

func (r ResourceRepository) RetryIngestion(ctx context.Context, ownerID, resourceID string, now time.Time) (resourceapp.IngestionStatus, bool, error) {
	var result resourceapp.IngestionStatus
	found := false
	err := r.withIngestionTx(ctx, func(tx ResourceRepository) error {
		if err := tx.requireIngestionOwner(ctx, ownerID); err != nil {
			return err
		}
		current, ok, err := tx.GetIngestion(ctx, ownerID, resourceID)
		if err != nil || !ok {
			return err
		}
		found = true
		result = current
		if !current.CanRetry {
			return nil
		}
		var modelID string
		if err := tx.DB().QueryRow(ctx, `SELECT id FROM public.embedding_model_versions WHERE logical_name='resource_embedding' AND status='active'`).Scan(&modelID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return resourceapp.ErrIngestionModelUnavailable
			}
			return err
		}
		var retryBuild bool
		if err := tx.DB().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM (`+ingestionJobStateSQL+`) j
			JOIN public.vector_index_generations g ON g.id=j.generation_id WHERE j.id=$1 AND g.model_version_id=$2
			AND g.state IN ('building','ready'))`, current.JobID, modelID).Scan(&retryBuild); err != nil {
			return err
		}
		g, err := tx.ingestionGenerationForModel(ctx, current.KnowledgeBaseID, modelID, retryBuild, now)
		if err != nil {
			return err
		}
		kind := resourceapp.IngestionJobIngest
		if current.PublicationStatus == "published" {
			kind = resourceapp.IngestionJobRebuild
		}
		if err := tx.enqueueIngestion(ctx, kind, resourceID, current.DocumentVersionID, g, now); err != nil {
			return err
		}
		_, err = tx.DB().Exec(ctx, `UPDATE public.document_versions SET process_status='queued',index_status='pending',error_code=NULL,error_message=NULL WHERE id=$1 AND published_at IS NULL`, current.DocumentVersionID)
		if err != nil {
			return err
		}
		result, _, err = tx.GetIngestion(ctx, ownerID, resourceID)
		return err
	})
	return result, found, err
}

func (r ResourceRepository) WithdrawIngestion(ctx context.Context, ownerID, resourceID string, deleted bool, now time.Time) (resourceapp.IngestionStatus, bool, error) {
	var result resourceapp.IngestionStatus
	found := false
	err := r.withIngestionTx(ctx, func(tx ResourceRepository) error {
		if err := tx.requireIngestionOwner(ctx, ownerID); err != nil {
			return err
		}
		current, ok, err := tx.GetIngestion(ctx, ownerID, resourceID)
		if err != nil || !ok {
			return err
		}
		found = true
		result = current
		if current.State == "deleted" || (!deleted && current.State == "unpublished") {
			return nil
		}
		_, err = tx.DB().Exec(ctx, `UPDATE public.contents SET status='ARCHIVED',deleted_at=CASE WHEN $2 THEN $3 ELSE deleted_at END,updated_at=$3 WHERE id=$1`, resourceID, deleted, now)
		if err != nil {
			return err
		}
		_, err = tx.DB().Exec(ctx, `UPDATE public.resource_memberships SET status='removed',updated_at=$2 WHERE resource_id=$1`, resourceID, now)
		if err != nil {
			return err
		}
		_, err = tx.DB().Exec(ctx, `UPDATE public.resource_documents SET status=CASE WHEN $2 THEN 'deleted' ELSE status END,deleted_at=CASE WHEN $2 THEN $3 ELSE deleted_at END,updated_at=$3 WHERE resource_id=$1`, resourceID, deleted, now)
		if err != nil {
			return err
		}
		_, err = tx.DB().Exec(ctx, `UPDATE public.document_versions v SET index_status='retired',deleted_at=CASE WHEN $2 THEN $3 ELSE v.deleted_at END
			FROM public.resource_documents d WHERE d.id=v.document_id AND d.resource_id=$1`, resourceID, deleted, now)
		if err != nil {
			return err
		}
		_, err = tx.DB().Exec(ctx, `WITH cancelled AS (UPDATE public.resource_processing_jobs SET status='cancelled',stage='cancelled',claimed_by=NULL,lease_expires_at=NULL,updated_at=$2,finished_at=$2
			WHERE resource_id=$1 AND job_type<>'purge' AND status IN ('pending','running','failed','dead') RETURNING outbox_event_id)
			UPDATE public.outbox_events SET processed_at=$2,lease_owner=NULL,lease_expires_at=NULL WHERE id IN(SELECT outbox_event_id FROM cancelled)`, resourceID, now)
		if err != nil {
			return err
		}
		_, err = tx.DB().Exec(ctx, `UPDATE public.resource_documents SET last_ingestion_job=last_ingestion_job ||
			jsonb_build_object('status','cancelled','stage','cancelled','updated_at',$2::timestamp)
			WHERE resource_id=$1 AND last_ingestion_job->>'job_type'<>'purge'
			AND last_ingestion_job->>'status' IN ('pending','running','failed','dead')`, resourceID, now)
		if err != nil {
			return err
		}
		rows, err := tx.DB().Query(ctx, ingestionGenerationSelect+` WHERE g.knowledge_base_id=$1 AND g.tenant_id=$2 ORDER BY g.id`, current.KnowledgeBaseID, resourceSearchDefaultTenantID)
		if err != nil {
			return err
		}
		var generations []resourceapp.IngestionGeneration
		for rows.Next() {
			g, err := scanIngestionGeneration(rows)
			if err != nil {
				rows.Close()
				return err
			}
			generations = append(generations, g)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, g := range generations {
			if err := tx.enqueueIngestion(ctx, resourceapp.IngestionJobPurge, resourceID, current.DocumentVersionID, g, now); err != nil {
				return err
			}
			if err := tx.activateIngestionGeneration(ctx, g, now); err != nil {
				return err
			}
		}
		result, _, err = tx.GetIngestion(ctx, ownerID, resourceID)
		return err
	})
	return result, found, err
}
