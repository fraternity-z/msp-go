package resource

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type IngestionManifestInspector interface {
	GetIngestionManifests(context.Context, string, []string) ([]IngestionManifest, error)
}

type IngestionReconcileReport struct {
	GenerationID string `json:"generation_id"`
	DryRun       bool   `json:"dry_run"`
	Desired      int    `json:"desired"`
	Missing      int    `json:"missing"`
	Mismatched   int    `json:"mismatched"`
	Extra        int    `json:"extra"`
	Scheduled    int    `json:"scheduled"`
	Removed      int    `json:"removed"`
	PointCount   int64  `json:"point_count"`
	NextOffset   string `json:"next_offset,omitempty"`
	Complete     bool   `json:"complete"`
}

// Reconcile compares current PostgreSQL identities with bounded provider pages.
func (w *IngestionWorker) Reconcile(ctx context.Context, generation IngestionGeneration, apply bool, maxPages int) (IngestionReconcileReport, error) {
	report := IngestionReconcileReport{GenerationID: generation.ID, DryRun: !apply}
	inspector, ok := w.repo.(IngestionManifestInspector)
	if !ok || !isSearchUUID(generation.ID) || !isSearchUUID(generation.KnowledgeBaseID) || maxPages < 1 || maxPages > 10000 {
		return report, ErrIngestionInvalid
	}
	repairs := map[string]bool{}
	schedule := func(versionID string) error {
		if !apply || repairs[versionID] {
			return nil
		}
		changed, err := w.repo.ScheduleIngestionRepair(ctx, generation.ID, versionID, time.Now().UTC())
		if err != nil {
			return err
		}
		repairs[versionID] = true
		if changed {
			report.Scheduled++
		}
		return nil
	}
	cursor := ingestionReconcileCursor{Phase: "manifests"}
	if apply && generation.ReconcileCursor != "" {
		if strings.HasPrefix(generation.ReconcileCursor, "{") {
			if json.Unmarshal([]byte(generation.ReconcileCursor), &cursor) != nil || (cursor.Phase != "manifests" && cursor.Phase != "vectors") || len(cursor.After) > 36 {
				return report, ErrIngestionInvalid
			}
		} else {
			cursor = ingestionReconcileCursor{Phase: "vectors", After: generation.ReconcileCursor}
		}
	}
	save := func(phase, after string) error {
		cursor = ingestionReconcileCursor{Phase: phase, After: after}
		encoded, _ := json.Marshal(cursor)
		report.NextOffset = string(encoded)
		if phase == "" {
			report.NextOffset = ""
		}
		if apply {
			return w.repo.SaveIngestionReconcileCursor(ctx, generation.ID, report.NextOffset)
		}
		return nil
	}
	after := cursor.After
	for page := 0; cursor.Phase == "manifests" && page < maxPages; page++ {
		manifests, err := w.repo.ListIngestionManifests(ctx, generation.ID, after, 128)
		if err != nil {
			return report, err
		}
		ids := make([]string, 0, len(manifests))
		for _, manifest := range manifests {
			if manifest.Desired {
				report.Desired++
			}
			if manifest.Desired && manifest.State == "indexed" {
				ids = append(ids, manifest.ID)
			}
		}
		if len(ids) > 0 {
			points, err := w.index.GetPoints(ctx, generation.Collection, ids)
			if err != nil && !errors.Is(err, ErrVectorNotFound) {
				return report, err
			}
			actual := make(map[string]VectorCandidate, len(points))
			for _, point := range points {
				actual[point.ID] = point
			}
			for _, manifest := range manifests {
				if !manifest.Desired || manifest.State != "indexed" {
					continue
				}
				point, found := actual[manifest.ID]
				if !found {
					report.Missing++
				} else if !manifestPointMatches(generation, manifest, point) {
					report.Mismatched++
				} else {
					continue
				}
				if err := schedule(manifest.DocumentVersionID); err != nil {
					return report, err
				}
			}
		}
		if len(manifests) < 128 {
			if err := save("vectors", ""); err != nil {
				return report, err
			}
			break
		}
		after = manifests[len(manifests)-1].ID
		if err := save("manifests", after); err != nil {
			return report, err
		}
	}
	if cursor.Phase == "manifests" {
		return report, nil
	}
	offset := cursor.After
	for pageNumber := 0; pageNumber < maxPages; pageNumber++ {
		page, err := w.index.ScrollPoints(ctx, VectorScrollRequest{Route: generation.Collection, Filter: ingestionPointFilter(generation, "", ""), Offset: offset, Limit: 128})
		if errors.Is(err, ErrVectorNotFound) {
			report.Complete = true
			return report, save("", "")
		}
		if err != nil {
			return report, err
		}
		ids := make([]string, len(page.Points))
		for i, point := range page.Points {
			ids[i] = point.ID
		}
		var manifests []IngestionManifest
		if len(ids) > 0 {
			manifests, err = inspector.GetIngestionManifests(ctx, generation.ID, ids)
			if err != nil {
				return report, err
			}
		}
		current := make(map[string]IngestionManifest, len(manifests))
		for _, manifest := range manifests {
			current[manifest.ID] = manifest
		}
		remove := []string{}
		for _, point := range page.Points {
			manifest, found := current[point.ID]
			if !found || !manifest.Desired {
				report.Extra++
				remove = append(remove, point.ID)
				continue
			}
			if manifest.State == "indexed" && !manifestPointMatches(generation, manifest, point) {
				if err := schedule(manifest.DocumentVersionID); err != nil {
					return report, err
				}
			}
		}
		if apply && len(remove) > 0 {
			if err := w.index.Delete(ctx, VectorDeleteRequest{Route: generation.Collection, IDs: remove, Wait: true}); err != nil {
				return report, err
			}
			report.Removed += len(remove)
		}
		if page.NextOffset == "" {
			if err := save("", ""); err != nil {
				return report, err
			}
			report.Complete = true
			break
		}
		if err := save("vectors", page.NextOffset); err != nil {
			return report, err
		}
		offset = page.NextOffset
	}
	count, err := w.index.CountPoints(ctx, generation.Collection, ingestionPointFilter(generation, "", ""))
	if err != nil {
		return report, err
	}
	report.PointCount = count
	return report, nil
}

type ingestionReconcileCursor struct {
	Phase string `json:"phase"`
	After string `json:"after,omitempty"`
}

func manifestPointMatches(generation IngestionGeneration, manifest IngestionManifest, point VectorCandidate) bool {
	return point.ID == manifest.ID && ingestionPayloadMatches(point.Payload, map[string]any{
		"tenant_id": generation.TenantID, "knowledge_base_id": generation.KnowledgeBaseID, "resource_id": manifest.ResourceID,
		"document_version_id": manifest.DocumentVersionID, "chunk_id": manifest.ChunkID, "generation_id": generation.ID,
		"index_generation": generation.Number, "model_version_id": generation.Model.ID, "visibility": "published", "embedding_sha256": manifest.EmbeddingSHA256,
		"content_sha256": manifest.ContentSHA256,
	})
}
