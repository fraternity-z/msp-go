package resource

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
)

var (
	ErrRerankDisabled    = errors.New("resource rerank is not configured")
	ErrRerankUnavailable = errors.New("resource rerank unavailable")
)

// QueryEmbedder must resolve and verify the administrator's active model itself.
type QueryEmbedder interface {
	EmbedQuery(context.Context, SearchScope, string) ([]float32, error)
}

// VectorManifestIdentity binds the Qdrant point ID to PostgreSQL's current index.
type VectorManifestIdentity struct {
	PointID           string
	ChunkID           string
	ResourceID        string
	DocumentVersionID string
	Generation        int64
	ModelVersionID    string
}

// VectorManifestResolver returns only currently authorized, indexed manifests.
type VectorManifestResolver interface {
	ResolveVectorManifests(context.Context, SearchScope, []string) ([]VectorManifestIdentity, error)
}

// SearchReranker consumes already authorized text and returns an ordered permutation.
// Callers must authorize the returned identities again before exposing any text.
type SearchReranker interface {
	Rerank(context.Context, string, []AuthorizedSearchChunk) ([]SearchCandidate, error)
}

type VectorRetriever struct {
	index     VectorIndex
	embedder  QueryEmbedder
	manifests VectorManifestResolver
}

func NewVectorRetriever(index VectorIndex, embedder QueryEmbedder, manifests VectorManifestResolver) (*VectorRetriever, error) {
	if index == nil || embedder == nil || manifests == nil {
		return nil, errors.New("resource vector retrieval dependencies are required")
	}
	return &VectorRetriever{index: index, embedder: embedder, manifests: manifests}, nil
}

func (r *VectorRetriever) RetrieveCandidates(ctx context.Context, scope SearchScope, query string, limit int) ([]SearchCandidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r == nil || r.index == nil || r.embedder == nil || r.manifests == nil {
		return nil, ErrVectorUnavailable
	}
	if scope.ResourceLimitExceeded || len(scope.ResourceIDs) > 1000 {
		return nil, ErrVectorUnavailable
	}
	if !isSearchUUID(scope.TenantID) || !isSearchUUID(scope.KnowledgeBaseID) ||
		!isSearchUUID(scope.GenerationID) || !isSearchUUID(scope.ModelVersionID) ||
		scope.Generation < 1 || scope.Dimension < 1 || scope.Dimension > 65536 ||
		strings.TrimSpace(scope.Collection) == "" || limit < 1 || limit > maxSearchCandidates ||
		!validSearchText(query, 2000) || strings.TrimSpace(query) == "" {
		return nil, ErrVectorInvalid
	}
	allowedResources := make(map[string]bool, len(scope.ResourceIDs))
	resourceIDs := make([]string, 0, len(scope.ResourceIDs))
	for _, id := range scope.ResourceIDs {
		if !isSearchUUID(id) {
			return nil, ErrVectorInvalid
		}
		if !allowedResources[id] {
			allowedResources[id] = true
			resourceIDs = append(resourceIDs, id)
		}
	}
	if len(resourceIDs) == 0 {
		return []SearchCandidate{}, nil
	}
	vector, err := r.embedder.EmbedQuery(ctx, scope, query)
	if err != nil {
		return nil, vectorBoundaryError(ctx, ErrEmbeddingFailed)
	}
	if len(vector) != scope.Dimension || !finiteQueryVector(vector) {
		return nil, ErrEmbeddingFailed
	}
	points, err := r.index.Search(ctx, VectorSearchRequest{
		Route: scope.Collection, Values: vector, Limit: limit,
		Filter: map[string]any{"must": []any{
			vectorMatch("tenant_id", scope.TenantID),
			vectorMatch("knowledge_base_id", scope.KnowledgeBaseID),
			vectorMatch("generation_id", scope.GenerationID),
			vectorMatch("visibility", "published"),
			map[string]any{"key": "resource_id", "match": map[string]any{"any": resourceIDs}},
		}},
		PayloadFields: []string{"tenant_id", "knowledge_base_id", "generation_id", "index_generation", "visibility", "resource_id", "document_version_id", "chunk_id", "model_version_id"},
	})
	if err != nil {
		return nil, vectorBoundaryError(ctx, ErrVectorUnavailable)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(points) > limit {
		return nil, ErrVectorUnavailable
	}
	pointIDs := make([]string, 0, len(points))
	verified := make([]VectorCandidate, 0, len(points))
	seen := make(map[string]bool, len(points))
	for _, point := range points {
		if !validVectorSearchPoint(point, scope, allowedResources) || seen[point.ID] {
			continue
		}
		seen[point.ID] = true
		pointIDs = append(pointIDs, point.ID)
		verified = append(verified, point)
	}
	if len(pointIDs) == 0 {
		return []SearchCandidate{}, nil
	}
	manifests, err := r.manifests.ResolveVectorManifests(ctx, scope, pointIDs)
	if err != nil {
		return nil, vectorBoundaryError(ctx, ErrVectorUnavailable)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	byPoint := make(map[string]VectorManifestIdentity, len(manifests))
	for _, manifest := range manifests {
		if seen[manifest.PointID] {
			byPoint[manifest.PointID] = manifest
		}
	}
	result := make([]SearchCandidate, 0, len(verified))
	for _, point := range verified {
		manifest, ok := byPoint[point.ID]
		if !ok || manifest.ChunkID != point.Payload["chunk_id"] ||
			manifest.ResourceID != point.Payload["resource_id"] || manifest.DocumentVersionID != point.Payload["document_version_id"] ||
			manifest.Generation != scope.Generation || manifest.ModelVersionID != scope.ModelVersionID {
			continue
		}
		result = append(result, SearchCandidate{ChunkID: manifest.ChunkID, ResourceID: manifest.ResourceID,
			DocumentVersionID: manifest.DocumentVersionID, Generation: manifest.Generation, Score: point.Score})
	}
	return result, nil
}

func vectorMatch(key string, value any) map[string]any {
	return map[string]any{"key": key, "match": map[string]any{"value": value}}
}

func validVectorSearchPoint(point VectorCandidate, scope SearchScope, resources map[string]bool) bool {
	if !isSearchUUID(point.ID) || math.IsNaN(point.Score) || math.IsInf(point.Score, 0) ||
		point.Payload["tenant_id"] != scope.TenantID || point.Payload["knowledge_base_id"] != scope.KnowledgeBaseID ||
		point.Payload["generation_id"] != scope.GenerationID || point.Payload["model_version_id"] != scope.ModelVersionID ||
		point.Payload["visibility"] != "published" || !payloadGenerationMatches(point.Payload["index_generation"], scope.Generation) {
		return false
	}
	resourceID, _ := point.Payload["resource_id"].(string)
	chunkID, _ := point.Payload["chunk_id"].(string)
	versionID, _ := point.Payload["document_version_id"].(string)
	return resources[resourceID] && isSearchUUID(chunkID) && isSearchUUID(versionID)
}

func payloadGenerationMatches(value any, generation int64) bool {
	switch typed := value.(type) {
	case float64:
		return typed > 0 && typed <= 1<<53 && typed == float64(generation) && int64(typed) == generation
	case int64:
		return typed == generation
	case int:
		return int64(typed) == generation
	case json.Number:
		parsed, err := strconv.ParseInt(string(typed), 10, 64)
		return err == nil && parsed == generation
	default:
		return false
	}
}

func finiteQueryVector(vector []float32) bool {
	var nonzero bool
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return false
		}
		nonzero = nonzero || value != 0
	}
	return nonzero
}

func vectorBoundaryError(ctx context.Context, fallback error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fallback
}
