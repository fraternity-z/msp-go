package postgres

import (
	"context"
	"time"

	resourceapp "mathstudy/backend/internal/application/resource"
)

// Compact outcomes retain status, retry routing and generation barriers, not execution payloads.
const ingestionJobStateSQL = `SELECT id,tenant_id,job_type,resource_id,document_version_id,generation_id,status,stage,
	attempt_count,max_attempts,last_error_code,created_at,updated_at FROM public.resource_processing_jobs
	UNION ALL
	SELECT archived.* FROM public.resource_documents d CROSS JOIN LATERAL jsonb_to_record(d.last_ingestion_job) AS archived(
		id varchar(36),tenant_id varchar(36),job_type varchar(32),resource_id varchar(36),document_version_id varchar(36),generation_id varchar(36),
		status varchar(32),stage varchar(32),attempt_count int,max_attempts int,last_error_code varchar(100),created_at timestamp,updated_at timestamp)
	WHERE archived.id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM public.resource_processing_jobs live
		WHERE live.id=archived.id OR (live.resource_id=archived.resource_id AND (live.created_at,live.id)>(archived.created_at,archived.id)))`

// CleanupIngestionHistory removes at most limit terminal jobs and limit terminal outbox events.
func (r ResourceRepository) CleanupIngestionHistory(ctx context.Context, now time.Time, limit int) (resourceapp.IngestionHistoryCleanup, error) {
	var result resourceapp.IngestionHistoryCleanup
	if now.IsZero() || limit < 1 || limit > 1000 {
		return result, resourceapp.ErrIngestionInvalid
	}
	cutoff := now.Add(-30 * 24 * time.Hour)
	err := r.withIngestionTx(ctx, func(tx ResourceRepository) error {
		rows, err := tx.DB().Query(ctx, `SELECT id FROM public.resource_processing_jobs
			WHERE tenant_id=$1 AND generation_id IS NOT NULL AND status IN ('succeeded','failed','dead','cancelled')
			AND finished_at<$2 AND updated_at<$2 ORDER BY finished_at,id FOR UPDATE SKIP LOCKED LIMIT $3`, resourceSearchDefaultTenantID, cutoff, limit)
		if err != nil {
			return err
		}
		ids := make([]string, 0, limit)
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(ids) > 0 {
			_, err = tx.DB().Exec(ctx, `WITH latest AS (
				SELECT DISTINCT ON(resource_id) id,tenant_id,job_type,resource_id,document_version_id,generation_id,status,stage,
					attempt_count,max_attempts,last_error_code,created_at,updated_at
				FROM public.resource_processing_jobs WHERE id=ANY($1::varchar[]) ORDER BY resource_id,created_at DESC,id DESC)
				UPDATE public.resource_documents d SET last_ingestion_job=to_jsonb(latest)
				FROM latest WHERE d.resource_id=latest.resource_id AND d.tenant_id=latest.tenant_id
				AND (d.last_ingestion_job->>'id' IS NULL OR
					(latest.created_at,latest.id) >= ((d.last_ingestion_job->>'created_at')::timestamp,d.last_ingestion_job->>'id'))`, ids)
			if err != nil {
				return err
			}
			tag, err := tx.DB().Exec(ctx, `DELETE FROM public.resource_processing_jobs WHERE id=ANY($1::varchar[])`, ids)
			if err != nil {
				return err
			}
			result.Jobs = tag.RowsAffected()
		}
		tag, err := tx.DB().Exec(ctx, `WITH expired AS (SELECT e.id FROM public.outbox_events e
			WHERE e.tenant_id=$1 AND e.aggregate_type='resource_ingestion'
			AND coalesce(e.processed_at,e.dead_at)<$2
			AND NOT EXISTS(SELECT 1 FROM public.resource_processing_jobs j WHERE j.outbox_event_id=e.id)
			ORDER BY coalesce(e.processed_at,e.dead_at),e.id FOR UPDATE SKIP LOCKED LIMIT $3)
			DELETE FROM public.outbox_events e USING expired WHERE e.id=expired.id`, resourceSearchDefaultTenantID, cutoff, limit)
		if err != nil {
			return err
		}
		result.OutboxEvents = tag.RowsAffected()
		return nil
	})
	if err != nil {
		return resourceapp.IngestionHistoryCleanup{}, err
	}
	return result, nil
}
