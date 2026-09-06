package postgres

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	resourceapp "mathstudy/backend/internal/application/resource"
)

func (r ResourceRepository) loadIngestionChunks(ctx context.Context, versionID, generationID string) ([]resourceapp.IngestionChunk, error) {
	rows, err := r.DB().Query(ctx, `SELECT ch.id,coalesce(m.id,''),ch.content_sha256,ch.ordinal,ch.content,coalesce(ch.language,''),coalesce(ch.page_no,0),
		coalesce(ch.section_path,''),coalesce(ch.start_offset,0),coalesce(ch.end_offset,0),ch.token_count,parent.ordinal
		FROM public.document_chunks ch LEFT JOIN public.document_chunks parent ON parent.id=ch.parent_chunk_id
		LEFT JOIN public.chunk_vector_manifests m ON m.chunk_id=ch.id AND m.generation_id=$2
		WHERE ch.document_version_id=$1 AND ch.tenant_id=$3 AND ch.deleted_at IS NULL ORDER BY ch.ordinal`, versionID, generationID, resourceSearchDefaultTenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]resourceapp.IngestionChunk, 0)
	for rows.Next() {
		var item resourceapp.IngestionChunk
		if err := rows.Scan(&item.ID, &item.ManifestID, &item.ContentSHA256, &item.Draft.Ordinal, &item.Draft.Content, &item.Draft.Language, &item.Draft.Page, &item.Draft.SectionPath,
			&item.Draft.StartOffset, &item.Draft.EndOffset, &item.Draft.TokenCount, &item.Draft.ParentIndex); err != nil {
			return nil, err
		}
		if item.ManifestID == "" {
			item.ManifestID = ingestionID("manifest", item.ID, generationID)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func validIngestionDrafts(drafts []resourceapp.ChunkDraft) bool {
	if len(drafts) == 0 || len(drafts) > 20000 {
		return false
	}
	total := 0
	for i, d := range drafts {
		if d.Ordinal != i || !validIngestionText(d.Content, 64000, false) || !validIngestionText(d.Language, 32, true) || !validIngestionText(d.SectionPath, 1000, true) ||
			d.Page < 0 || d.StartOffset < 0 || d.EndOffset < d.StartOffset || d.TokenCount < 0 || d.ParentIndex != nil && (*d.ParentIndex < 0 || *d.ParentIndex >= i) {
			return false
		}
		total += len(d.Content)
		if total > 200<<20 {
			return false
		}
	}
	return true
}

// PrepareIngestionChunks persists deterministic identities before any vector IO.
func (r ResourceRepository) PrepareIngestionChunks(ctx context.Context, lease resourceapp.IngestionLease, drafts []resourceapp.ChunkDraft, now time.Time) ([]resourceapp.IngestionChunk, error) {
	if len(drafts) > 0 && !validIngestionDrafts(drafts) {
		return nil, resourceapp.ErrIngestionInvalid
	}
	var result []resourceapp.IngestionChunk
	err := r.withIngestionTx(ctx, func(tx ResourceRepository) error {
		work, err := tx.fencedIngestionWork(ctx, lease, now)
		if err != nil {
			return err
		}
		if work.Job.Type == resourceapp.IngestionJobPurge {
			return resourceapp.ErrIngestionInvalid
		}
		if len(work.Chunks) > 0 {
			if len(drafts) > 0 {
				if len(work.Chunks) != len(drafts) {
					return resourceapp.ErrIngestionConflict
				}
				for i, ch := range work.Chunks {
					if !reflect.DeepEqual(ch.Draft, drafts[i]) {
						return resourceapp.ErrIngestionConflict
					}
				}
			}
			result = work.Chunks
		} else {
			if !validIngestionDrafts(drafts) {
				return resourceapp.ErrIngestionInvalid
			}
			if work.Job.Type != resourceapp.IngestionJobIngest {
				return resourceapp.ErrIngestionConflict
			}
			result = make([]resourceapp.IngestionChunk, 0, len(drafts))
			for i, draft := range drafts {
				id := ingestionID("chunk", work.Job.DocumentVersionID, fmt.Sprint(i))
				hash := ingestionHash(draft.Content)
				var parent any
				if draft.ParentIndex != nil {
					parent = ingestionID("chunk", work.Job.DocumentVersionID, fmt.Sprint(*draft.ParentIndex))
				}
				_, err = tx.DB().Exec(ctx, `INSERT INTO public.document_chunks
					(id,document_version_id,tenant_id,ordinal,parent_chunk_id,content,content_sha256,token_count,language,page_no,section_path,start_offset,end_offset,created_at)
					VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, id, work.Job.DocumentVersionID, work.Job.TenantID, draft.Ordinal, parent, draft.Content, hash, draft.TokenCount,
					draft.Language, draft.Page, draft.SectionPath, draft.StartOffset, draft.EndOffset, now)
				if err != nil {
					return err
				}
				result = append(result, resourceapp.IngestionChunk{ID: id, ManifestID: ingestionID("manifest", id, work.Generation.ID), ContentSHA256: hash, Draft: draft})
			}
		}
		for _, chunk := range result {
			_, err = tx.DB().Exec(ctx, `INSERT INTO public.chunk_vector_manifests
				(id,chunk_id,tenant_id,generation_id,model_version_id,collection_name,index_generation,embedding_sha256,dimension,created_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
				ON CONFLICT(chunk_id,generation_id) DO NOTHING`, chunk.ManifestID, chunk.ID, work.Job.TenantID, work.Generation.ID, work.Generation.Model.ID,
				work.Generation.Collection, work.Generation.Number, strings.Repeat("0", 64), work.Generation.Model.Dimension, now)
			if err != nil {
				return err
			}
		}
		_, err = tx.DB().Exec(ctx, `UPDATE public.resource_processing_jobs SET stage='indexing',updated_at=$2 WHERE id=$1`, lease.JobID, now)
		if err != nil {
			return err
		}
		_, err = tx.DB().Exec(ctx, `UPDATE public.document_versions SET chunk_count=$2,process_status='succeeded',index_status='building' WHERE id=$1 AND published_at IS NULL`, work.Job.DocumentVersionID, len(result))
		return err
	})
	return result, err
}

func (r ResourceRepository) CompleteIngestionJob(ctx context.Context, lease resourceapp.IngestionLease, receipts []resourceapp.ManifestReceipt, now time.Time) (bool, error) {
	if !validIngestionLease(lease) || len(receipts) > 20000 {
		return false, resourceapp.ErrIngestionInvalid
	}
	verified := make(map[string]string, len(receipts))
	for _, receipt := range receipts {
		if !validResourceSearchID(receipt.ID) || !validIngestionHash(receipt.EmbeddingSHA256) {
			return false, resourceapp.ErrIngestionInvalid
		}
		if _, ok := verified[receipt.ID]; ok {
			return false, resourceapp.ErrIngestionInvalid
		}
		verified[receipt.ID] = receipt.EmbeddingSHA256
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
		if work.Job.Type == resourceapp.IngestionJobPurge {
			if len(receipts) != 0 {
				return resourceapp.ErrIngestionInvalid
			}
			_, err = tx.DB().Exec(ctx, `UPDATE public.chunk_vector_manifests m SET state='deleted',deleted_at=$3
				FROM public.document_chunks ch JOIN public.document_versions v ON v.id=ch.document_version_id
				JOIN public.resource_documents d ON d.id=v.document_id WHERE m.chunk_id=ch.id AND d.resource_id=$1 AND m.generation_id=$2`, work.Job.ResourceID, work.Generation.ID, now)
			if err != nil {
				return err
			}
		} else {
			if len(work.Chunks) == 0 || len(work.Chunks) != len(verified) {
				return resourceapp.ErrIngestionInvalid
			}
			for _, chunk := range work.Chunks {
				if _, exists := verified[chunk.ManifestID]; !exists {
					return resourceapp.ErrIngestionInvalid
				}
			}
			var ignored any
			if err := tx.DB().QueryRow(ctx, `SELECT pg_advisory_xact_lock(hashtext('resource_embedding'))`).Scan(&ignored); err != nil {
				return err
			}
			var active bool
			if err := tx.DB().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.embedding_model_versions WHERE id=$1 AND status='active' AND logical_name='resource_embedding')`, work.Generation.Model.ID).Scan(&active); err != nil {
				return err
			}
			if !active {
				return resourceapp.ErrIngestionModelUnavailable
			}
			for _, chunk := range work.Chunks {
				tag, err := tx.DB().Exec(ctx, `UPDATE public.chunk_vector_manifests SET state='indexed',embedding_sha256=$2,indexed_at=$3,deleted_at=NULL
					WHERE id=$1 AND chunk_id=$4 AND generation_id=$5 AND model_version_id=$6 AND dimension=$7 AND collection_name=$8 AND index_generation=$9 AND tenant_id=$10`,
					chunk.ManifestID, verified[chunk.ManifestID], now, chunk.ID, work.Generation.ID, work.Generation.Model.ID, work.Generation.Model.Dimension, work.Generation.Collection, work.Generation.Number, work.Job.TenantID)
				if err != nil {
					return err
				}
				if tag.RowsAffected() != 1 {
					return resourceapp.ErrIngestionConflict
				}
			}
			if work.Generation.State == "active" {
				if err := tx.publishIngestionVersion(ctx, work, now); err != nil {
					return err
				}
			}
		}
		_, err = tx.DB().Exec(ctx, `WITH completed AS (UPDATE public.resource_processing_jobs SET status='succeeded',stage='completed',claimed_by=NULL,lease_expires_at=NULL,
			last_error_code=NULL,last_error_message=NULL,finished_at=$2,updated_at=$2 WHERE id=$1 RETURNING outbox_event_id)
			UPDATE public.outbox_events SET processed_at=$2,lease_owner=NULL,lease_expires_at=NULL,error_code=NULL,last_error=NULL,dead_at=NULL WHERE id IN(SELECT outbox_event_id FROM completed)`, lease.JobID, now)
		if err != nil {
			return err
		}
		if err := tx.activateIngestionGeneration(ctx, work.Generation, now); err != nil {
			return err
		}
		if work.Job.Type != resourceapp.IngestionJobPurge {
			// A document published while another generation builds becomes part of that build.
			rows, err := tx.DB().Query(ctx, ingestionGenerationSelect+` WHERE g.knowledge_base_id=$1 AND g.state='building' AND g.id<>$2`, work.Generation.KnowledgeBaseID, work.Generation.ID)
			if err != nil {
				return err
			}
			var pending []resourceapp.IngestionGeneration
			for rows.Next() {
				g, err := scanIngestionGeneration(rows)
				if err != nil {
					rows.Close()
					return err
				}
				pending = append(pending, g)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			for _, g := range pending {
				if err := tx.enqueueIngestion(ctx, resourceapp.IngestionJobRebuild, work.Job.ResourceID, work.Job.DocumentVersionID, g, now); err != nil {
					return err
				}
			}
		}
		ok = true
		return nil
	})
	return ok, err
}

func (r ResourceRepository) publishIngestionVersion(ctx context.Context, work resourceapp.IngestionWork, now time.Time) error {
	_, err := r.DB().Exec(ctx, `UPDATE public.document_versions SET process_status='succeeded',index_status='ready',chunk_count=$2,
		index_generation=$3,model_version_id=$4,published_at=coalesce(published_at,$5),error_code=NULL,error_message=NULL WHERE id=$1`,
		work.Job.DocumentVersionID, len(work.Chunks), work.Generation.Number, work.Generation.Model.ID, now)
	if err != nil {
		return err
	}
	_, err = r.DB().Exec(ctx, `UPDATE public.contents SET status='PUBLISHED',published_at=coalesce(published_at,$2),updated_at=$2 WHERE id=$1 AND deleted_at IS NULL AND status IN ('DRAFT','PUBLISHED')`, work.Job.ResourceID, now)
	return err
}
