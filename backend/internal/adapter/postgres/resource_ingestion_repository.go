package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	resourceapp "mathstudy/backend/internal/application/resource"
	"mathstudy/backend/internal/platform/uploadpath"
)

var _ resourceapp.IngestionRepository = ResourceRepository{}

// Metadata transactions share one short lock; parsing and external IO never hold it.
func (r ResourceRepository) withIngestionTx(ctx context.Context, fn func(ResourceRepository) error) error {
	return r.withTx(ctx, func(tx ResourceRepository) error {
		var ignored any
		if err := tx.DB().QueryRow(ctx, `SELECT pg_advisory_xact_lock(hashtext('resource_ingestion'))`).Scan(&ignored); err != nil {
			return err
		}
		return fn(tx)
	})
}

// RegisterIngestion atomically records the draft, immutable source and durable job.
func (r ResourceRepository) RegisterIngestion(ctx context.Context, ownerID string, input resourceapp.IngestionRegistration, now time.Time) (resourceapp.IngestionStatus, bool, error) {
	if !validResourceSearchID(ownerID) || !validResourceSearchID(input.KnowledgeBaseID) || !validResourceSearchID(input.ModelVersionID) ||
		!validIngestionText(input.Title, 500, false) || !validIngestionText(input.Chapter, 200, true) || !validIngestionText(input.Topic, 200, true) ||
		!validIngestionText(input.IdempotencyKey, 200, false) || !validIngestionText(input.Metadata.Filename, 255, false) ||
		!validIngestionText(input.Metadata.MIMEType, 127, false) || input.Metadata.ByteSize <= 0 || input.Metadata.ByteSize > resourceapp.MaxDocumentBytes ||
		!validIngestionHash(input.Metadata.Checksum) || !validIngestionText(input.Source.StorageKey, 500, false) {
		return resourceapp.IngestionStatus{}, false, resourceapp.ErrIngestionInvalid
	}
	parsed, err := url.Parse(input.Source.URI)
	if err != nil || parsed.User != nil || input.Source.URI == "" || len(input.Source.URI) > 1000 {
		return resourceapp.IngestionStatus{}, false, resourceapp.ErrIngestionInvalid
	}
	parsed.RawQuery, parsed.Fragment = "", ""
	input.Source.URI = parsed.String()
	if !uploadpath.IsDocumentPath(input.Source.URI) && ((parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "") {
		return resourceapp.IngestionStatus{}, false, resourceapp.ErrIngestionInvalid
	}
	fingerprintData, _ := json.Marshal([]any{input.Title, input.Chapter, input.Topic, input.KnowledgeBaseID, input.Metadata})
	fingerprint := ingestionHash(string(fingerprintData))
	var result resourceapp.IngestionStatus
	created := false
	err = r.withIngestionTx(ctx, func(tx ResourceRepository) error {
		if err := tx.requireIngestionOwner(ctx, ownerID); err != nil {
			return err
		}
		var deleting bool
		if err := tx.DB().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.resource_ingestion_uploads
			WHERE source_uri=$1 AND storage_key=$2 AND state='deleting')`, input.Source.URI, input.Source.StorageKey).Scan(&deleting); err != nil {
			return err
		}
		if deleting {
			return resourceapp.ErrIngestionConflict
		}
		var resourceID, previousHash string
		err := tx.DB().QueryRow(ctx, `SELECT resource_id, registration_sha256 FROM public.resource_documents
			WHERE created_by = $1 AND registration_key = $2`, ownerID, input.IdempotencyKey).Scan(&resourceID, &previousHash)
		if err == nil {
			if previousHash != fingerprint {
				return resourceapp.ErrIngestionConflict
			}
			result, _, err = tx.GetIngestion(ctx, ownerID, resourceID)
			if err != nil {
				return err
			}
			return tx.clearReferencedIngestionUpload(ctx, input.Source)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		limit := input.QueueLimit
		if limit <= 0 {
			limit = 1000
		}
		var count int
		if err := tx.DB().QueryRow(ctx, `SELECT count(*)::int FROM public.resource_processing_jobs WHERE status IN ('pending','running')`).Scan(&count); err != nil {
			return err
		}
		if count >= limit {
			return resourceapp.ErrIngestionQueueFull
		}
		generation, err := tx.ingestionGenerationForModel(ctx, input.KnowledgeBaseID, input.ModelVersionID, false, now)
		if err != nil {
			return err
		}
		resourceID, err = newUUID()
		if err != nil {
			return err
		}
		documentID, err := newUUID()
		if err != nil {
			return err
		}
		versionID, err := newUUID()
		if err != nil {
			return err
		}
		storageType := "external"
		if uploadpath.IsLocalPath(input.Source.URI) {
			storageType = "local"
		}
		meta, _ := json.Marshal(map[string]any{"chapter": input.Chapter, "topic": input.Topic, "storage_type": storageType, "ingestion": true})
		_, err = tx.DB().Exec(ctx, `INSERT INTO public.contents
			(id,type,owner_teacher_id,status,title,body,difficulty,concept_ids,tags,meta,created_at,updated_at,tenant_id)
			VALUES ($1,'ARTICLE',$2,'DRAFT',$3,'',0,'[]','[]',$4::json,$5,$5,$6)`, resourceID, ownerID, input.Title, string(meta), now, generation.TenantID)
		if err != nil {
			return err
		}
		_, err = tx.DB().Exec(ctx, `INSERT INTO public.resource_memberships (tenant_id,knowledge_base_id,resource_id,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$4)`, generation.TenantID, generation.KnowledgeBaseID, resourceID, now)
		if err != nil {
			return err
		}
		_, err = tx.DB().Exec(ctx, `INSERT INTO public.resource_documents
			(id,resource_id,tenant_id,knowledge_base_id,object_uri,filename,mime_type,byte_size,checksum_sha256,created_by,created_at,updated_at,current_version_id,registration_key,registration_sha256)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11,$12,$13,$14)`, documentID, resourceID, generation.TenantID, generation.KnowledgeBaseID,
			input.Source.URI, input.Metadata.Filename, input.Metadata.MIMEType, input.Metadata.ByteSize, input.Metadata.Checksum, ownerID, now, versionID, input.IdempotencyKey, fingerprint)
		if err != nil {
			return err
		}
		sourceMeta, _ := json.Marshal(map[string]any{"storage_key": input.Source.StorageKey, "uri": input.Source.URI})
		_, err = tx.DB().Exec(ctx, `INSERT INTO public.document_versions
			(id,document_id,tenant_id,version_no,content_sha256,parser_name,parser_version,source_metadata,created_at)
			VALUES ($1,$2,$3,1,$4,'resource_document','1',$5::json,$6)`, versionID, documentID, generation.TenantID, input.Metadata.Checksum, string(sourceMeta), now)
		if err != nil {
			return err
		}
		if uploadpath.IsLocalPath(input.Source.URI) {
			access := UploadAccessRepository{Repository: tx.Repository}
			if err := access.RecordLocalUpload(ctx, ownerID, input.Source.URI); err != nil {
				return err
			}
		}
		if err := tx.enqueueIngestion(ctx, resourceapp.IngestionJobIngest, resourceID, versionID, generation, now); err != nil {
			return err
		}
		if err := tx.clearReferencedIngestionUpload(ctx, input.Source); err != nil {
			return err
		}
		result, _, err = tx.GetIngestion(ctx, ownerID, resourceID)
		created = err == nil
		return err
	})
	return result, created, err
}

func (r ResourceRepository) requireIngestionOwner(ctx context.Context, ownerID string) error {
	var id string
	err := r.DB().QueryRow(ctx, `SELECT id FROM public.users WHERE id=$1 AND role IN ('TEACHER','ADMIN')
		AND is_active=true AND status='ACTIVE' FOR KEY SHARE`, ownerID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return resourceapp.ErrAuthorizationDenied
	}
	return err
}

const ingestionStatusSelect = `SELECT c.id, v.id, d.knowledge_base_id, c.title, coalesce(d.filename,''),d.mime_type,d.byte_size,
	c.status::text, c.deleted_at IS NOT NULL, v.process_status,v.index_status,
	coalesce(j.id,''),coalesce(j.status,'pending'),coalesce(j.stage,'queued'),coalesce(j.attempt_count,0),coalesce(j.max_attempts,3),
	v.chunk_count,(SELECT count(*)::int FROM public.chunk_vector_manifests m JOIN public.document_chunks ch ON ch.id=m.chunk_id
		WHERE ch.document_version_id=v.id AND m.generation_id=j.generation_id AND m.state='indexed' AND m.deleted_at IS NULL),
	coalesce(j.last_error_code,''),c.created_at,greatest(c.updated_at,coalesce(j.updated_at,c.updated_at))
	FROM public.contents c JOIN public.users owner ON owner.id=c.owner_teacher_id
	JOIN public.resource_documents d ON d.resource_id=c.id AND d.tenant_id=c.tenant_id
	JOIN public.document_versions v ON v.id=d.current_version_id AND v.document_id=d.id
	LEFT JOIN LATERAL (SELECT job.* FROM (` + ingestionJobStateSQL + `) job WHERE job.resource_id=c.id
		ORDER BY job.created_at DESC,job.id DESC LIMIT 1) j ON true
	WHERE c.owner_teacher_id=$1 AND owner.is_active=true AND owner.status='ACTIVE' AND owner.role IN ('TEACHER','ADMIN')
	AND c.tenant_id='00000000-0000-4000-8000-000000000001'`

func (r ResourceRepository) GetIngestion(ctx context.Context, ownerID, resourceID string) (resourceapp.IngestionStatus, bool, error) {
	if !validResourceSearchID(ownerID) || !validResourceSearchID(resourceID) {
		return resourceapp.IngestionStatus{}, false, resourceapp.ErrIngestionInvalid
	}
	item, err := scanIngestionStatus(r.DB().QueryRow(ctx, ingestionStatusSelect+` AND c.id=$2`, ownerID, resourceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return resourceapp.IngestionStatus{}, false, nil
	}
	return item, err == nil, err
}

func (r ResourceRepository) ListIngestions(ctx context.Context, ownerID string, limit, offset int) ([]resourceapp.IngestionStatus, int, error) {
	if !validResourceSearchID(ownerID) || limit < 1 || limit > 100 || offset < 0 {
		return nil, 0, resourceapp.ErrIngestionInvalid
	}
	items := make([]resourceapp.IngestionStatus, 0)
	var total int
	if err := r.DB().QueryRow(ctx, `SELECT count(*)::int FROM (`+ingestionStatusSelect+`) owned`, ownerID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.DB().Query(ctx, ingestionStatusSelect+` ORDER BY c.created_at DESC,c.id DESC LIMIT $2 OFFSET $3`, ownerID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanIngestionStatus(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func scanIngestionStatus(row rowScanner) (resourceapp.IngestionStatus, error) {
	var result resourceapp.IngestionStatus
	var publication string
	var deleted bool
	err := row.Scan(&result.ResourceID, &result.DocumentVersionID, &result.KnowledgeBaseID, &result.Title, &result.Filename, &result.MIMEType, &result.ByteSize,
		&publication, &deleted, &result.ProcessStatus, &result.IndexStatus, &result.JobID, &result.JobStatus, &result.Stage, &result.Attempt, &result.MaxAttempts,
		&result.ChunkCount, &result.IndexedChunks, &result.ErrorCode, &result.CreatedAt, &result.UpdatedAt)
	if err != nil {
		return result, err
	}
	result.PublicationStatus = "draft"
	result.State = "queued"
	switch {
	case deleted:
		result.State = "deleted"
		result.PublicationStatus = "deleted"
	case publication == "ARCHIVED":
		result.State = "unpublished"
		result.PublicationStatus = "unpublished"
	case publication == "PUBLISHED":
		result.State = "published"
		result.PublicationStatus = "published"
	case result.JobStatus == "dead":
		result.State = "dead"
	case result.JobStatus == "failed":
		result.State = "failed"
	case result.JobStatus == "running":
		result.State = "processing"
	case result.JobStatus == "succeeded":
		result.State = "processing"
	}
	result.CanRetry = !deleted && publication != "ARCHIVED" && (result.JobStatus == "dead" || result.JobStatus == "failed")
	result.Retryable = result.CanRetry
	result.CanUnpublish = !deleted && publication != "ARCHIVED"
	result.CanDelete = !deleted
	return result, nil
}

func validIngestionText(value string, max int, empty bool) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, '\x00') && utf8.RuneCountInString(value) <= max && (empty || strings.TrimSpace(value) != "")
}

func validIngestionHash(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

func ingestionHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func ingestionID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}
