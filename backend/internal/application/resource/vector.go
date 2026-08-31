package resource

import (
	"context"
	"errors"
)

// VectorDistance is the provider-neutral distance metric used by an index.
type VectorDistance string

const (
	VectorDistanceCosine VectorDistance = "cosine"
	VectorDistanceDot    VectorDistance = "dot"
	VectorDistanceEuclid VectorDistance = "euclid"
)

// VectorPayloadIndex describes one filterable payload field.
type VectorPayloadIndex struct {
	Field string
	Kind  string
}

// VectorCollectionSpec describes the immutable shape of one vector index.
type VectorCollectionSpec struct {
	Route          string
	Dimension      int
	Distance       VectorDistance
	PayloadIndexes []VectorPayloadIndex
}

// VectorPoint is the provider-neutral point representation.
type VectorPoint struct {
	ID      string
	Values  []float32
	Payload map[string]any
}

// VectorBatch is an idempotent batch upsert request.
type VectorBatch struct {
	Route  string
	Points []VectorPoint
	Wait   bool
}

// VectorDeleteRequest removes points by ID or by a provider-neutral filter.
type VectorDeleteRequest struct {
	Route  string
	IDs    []string
	Filter map[string]any
	Wait   bool
}

// VectorSearchRequest contains one query vector and an optional payload filter.
type VectorSearchRequest struct {
	Route         string
	Values        []float32
	Limit         int
	Filter        map[string]any
	WithPayload   bool
	PayloadFields []string
}

// VectorCandidate is a search result with minimal payload only.
type VectorCandidate struct {
	ID      string
	Score   float64
	Payload map[string]any
}

// VectorCollectionStatus is the verified provider-side schema state.
type VectorCollectionStatus struct {
	Route      string
	Dimension  int
	Distance   VectorDistance
	PointCount int64
}

// VectorIndex is the application port for a durable vector store.
// Implementations must keep provider-specific request and response types out
// of the application package.
type VectorIndex interface {
	Ping(context.Context) error
	EnsureCollection(context.Context, VectorCollectionSpec) error
	VerifyCollection(context.Context, VectorCollectionSpec) (VectorCollectionStatus, error)
	Upsert(context.Context, VectorBatch) error
	Delete(context.Context, VectorDeleteRequest) error
	Search(context.Context, VectorSearchRequest) ([]VectorCandidate, error)
}

var (
	// ErrVectorUnavailable indicates a transient provider or network failure.
	ErrVectorUnavailable = errors.New("vector index unavailable")
	// ErrVectorInvalid indicates an invalid request or incompatible schema.
	ErrVectorInvalid = errors.New("invalid vector index request")
	// ErrVectorNotFound indicates that a requested collection or point is absent.
	ErrVectorNotFound = errors.New("vector index object not found")
)
