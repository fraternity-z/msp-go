package postgres

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	resourceapp "mathstudy/backend/internal/application/resource"
)

const ingestionUploadReferencedSQL = `EXISTS(SELECT 1 FROM public.resource_documents d
	WHERE d.object_uri=u.source_uri OR d.source_uri=u.source_uri)
	OR EXISTS(SELECT 1 FROM public.document_versions v WHERE v.source_metadata->>'uri'=u.source_uri)
	OR EXISTS(SELECT 1 FROM public.content_assets a WHERE a.url=u.source_uri)`

func (r ResourceRepository) StageIngestionUpload(ctx context.Context, ownerID string, source resourceapp.ObjectSource, now time.Time) error {
	parsed, err := url.Parse(source.URI)
	if !validIngestionUploadID(ownerID) || now.IsZero() || err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		!validIngestionText(source.URI, 1000, false) || !validIngestionText(source.StorageKey, 500, false) ||
		!strings.HasPrefix(source.StorageKey, "documents/ingestions/"+ownerID+"/") || strings.ContainsAny(source.StorageKey, "\\?#%:") ||
		strings.Contains(source.StorageKey, "..") || (!strings.HasPrefix(source.URI, "/uploads/") && ((parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "")) {
		return resourceapp.ErrIngestionInvalid
	}
	return r.withIngestionTx(ctx, func(tx ResourceRepository) error {
		if err := tx.requireIngestionOwner(ctx, ownerID); err != nil {
			return err
		}
		var occupied int
		var existing, referenced bool
		if err := tx.DB().QueryRow(ctx, `SELECT
			(SELECT count(*) FROM public.resource_processing_jobs WHERE status IN ('pending','running'))+
			(SELECT count(*) FROM public.resource_ingestion_uploads u WHERE NOT (`+ingestionUploadReferencedSQL+`)),
			EXISTS(SELECT 1 FROM public.resource_ingestion_uploads WHERE owner_id=$1 AND source_uri=$2 AND storage_key=$3),
			EXISTS(SELECT 1 FROM public.resource_documents WHERE created_by=$1 AND object_uri=$2)`, ownerID, source.URI, source.StorageKey).Scan(&occupied, &existing, &referenced); err != nil {
			return err
		}
		if referenced {
			return nil
		}
		if !existing && occupied >= 1000 {
			return resourceapp.ErrIngestionQueueFull
		}
		tag, err := tx.DB().Exec(ctx, `INSERT INTO public.resource_ingestion_uploads(id,tenant_id,owner_id,source_uri,storage_key,created_at,updated_at)
			VALUES($1,$2,$3,$4,$5,$6,$6) ON CONFLICT(owner_id,source_uri,storage_key) DO UPDATE SET updated_at=$6
			WHERE public.resource_ingestion_uploads.state='staging'`, ingestionID("upload", ownerID, source.URI, source.StorageKey), resourceSearchDefaultTenantID, ownerID, source.URI, source.StorageKey, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return resourceapp.ErrIngestionConflict
		}
		return nil
	})
}

func (r ResourceRepository) ClaimStaleIngestionUploads(ctx context.Context, now time.Time, limit int) ([]resourceapp.IngestionUploadCleanup, error) {
	if now.IsZero() || limit < 1 || limit > 100 {
		return nil, resourceapp.ErrIngestionInvalid
	}
	items := make([]resourceapp.IngestionUploadCleanup, 0, limit)
	err := r.withIngestionTx(ctx, func(tx ResourceRepository) error {
		// Referenced objects are never reclaimed, including withdrawn document sources.
		_, err := tx.DB().Exec(ctx, `WITH referenced AS (SELECT u.id FROM public.resource_ingestion_uploads u
			WHERE u.tenant_id=$1 AND (`+ingestionUploadReferencedSQL+`)
			ORDER BY u.updated_at,u.id LIMIT $2)
			DELETE FROM public.resource_ingestion_uploads u USING referenced WHERE u.id=referenced.id`, resourceSearchDefaultTenantID, limit)
		if err != nil {
			return err
		}
		token, err := newUUID()
		if err != nil {
			return err
		}
		rows, err := tx.DB().Query(ctx, `WITH stale AS (SELECT u.id FROM public.resource_ingestion_uploads u
			WHERE u.tenant_id=$1 AND ((u.state='staging' AND u.updated_at<$2) OR (u.state='deleting' AND u.lease_expires_at<=$3))
			AND NOT (`+ingestionUploadReferencedSQL+`) ORDER BY u.updated_at,u.id FOR UPDATE SKIP LOCKED LIMIT $4)
			UPDATE public.resource_ingestion_uploads u SET state='deleting',lease_token=$5,lease_expires_at=$6
			FROM stale WHERE u.id=stale.id RETURNING u.id,u.source_uri,u.storage_key,u.lease_token`, resourceSearchDefaultTenantID, now.Add(-24*time.Hour), now, limit, token, now.Add(15*time.Minute))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item resourceapp.IngestionUploadCleanup
			if err := rows.Scan(&item.ID, &item.Source.URI, &item.Source.StorageKey, &item.LeaseToken); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return items, nil
}

func (r ResourceRepository) FinishIngestionUploadCleanup(ctx context.Context, id, leaseToken string, now time.Time) (bool, error) {
	if !validIngestionUploadID(id) || !validIngestionUploadID(leaseToken) || now.IsZero() {
		return false, resourceapp.ErrIngestionInvalid
	}
	tag, err := r.DB().Exec(ctx, `DELETE FROM public.resource_ingestion_uploads WHERE id=$1 AND lease_token=$2 AND state='deleting'
		AND lease_expires_at>$3 AND tenant_id=$4`, id, leaseToken, now, resourceSearchDefaultTenantID)
	return tag.RowsAffected() == 1, err
}

func validIngestionUploadID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}

func (r ResourceRepository) clearReferencedIngestionUpload(ctx context.Context, source resourceapp.ObjectSource) error {
	_, err := r.DB().Exec(ctx, `DELETE FROM public.resource_ingestion_uploads u
		WHERE u.source_uri=$1 AND u.storage_key=$2 AND (`+ingestionUploadReferencedSQL+`)`, source.URI, source.StorageKey)
	return err
}
