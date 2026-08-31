package resource

import (
	"context"
	"errors"
	"io"
	"time"
)

// ObjectSource identifies an object without exposing a storage SDK to the
// application layer.
type ObjectSource struct {
	URI        string
	StorageKey string
}

// ObjectMetadata contains only bounded metadata needed by parsers.
type ObjectMetadata struct {
	Filename string
	MIMEType string
	ByteSize int64
	Checksum string
}

// ObjectReader opens a source object and leaves ownership of the reader with
// the caller.
type ObjectReader interface {
	Open(context.Context, ObjectSource) (io.ReadCloser, ObjectMetadata, error)
}

// ParseInput is the provider-neutral parser input contract.
type ParseInput struct {
	Reader   io.Reader
	Metadata ObjectMetadata
}

// ParsedDocument is a normalized document tree.  Full source text remains in
// PostgreSQL; this value is bounded by the parser policy at the boundary.
type ParsedDocument struct {
	Title    string
	Language string
	Blocks   []DocumentBlock
}

// DocumentBlock is a structural unit such as a heading, paragraph or table.
type DocumentBlock struct {
	Kind        string
	Text        string
	Page        int
	SectionPath string
}

// DocumentParser converts an object into normalized structural blocks.
type DocumentParser interface {
	Parse(context.Context, ParseInput) (ParsedDocument, error)
}

// ChunkPolicy controls deterministic chunk boundaries.
type ChunkPolicy struct {
	MaxTokens     int
	OverlapTokens int
	MaxCharacters int
}

// ChunkDraft is the persistence-ready, provider-neutral chunk value.
type ChunkDraft struct {
	Ordinal     int
	ParentIndex *int
	Content     string
	Language    string
	Page        int
	SectionPath string
	StartOffset int
	EndOffset   int
	TokenCount  int
}

// Chunker splits a parsed document without performing storage or embedding IO.
type Chunker interface {
	Chunk(context.Context, ParsedDocument, ChunkPolicy) ([]ChunkDraft, error)
}

// EmbeddingModel identifies an immutable provider/model/revision contract.
type EmbeddingModel struct {
	ID        string
	Provider  string
	Model     string
	Revision  string
	Dimension int
	Distance  VectorDistance
	MaxTokens int
}

// EmbeddingRequest is a bounded batch for one immutable model version.
type EmbeddingRequest struct {
	Model  EmbeddingModel
	Inputs []string
}

// EmbeddingResponse preserves input order and model identity.
type EmbeddingResponse struct {
	Model   EmbeddingModel
	Vectors [][]float32
}

// EmbeddingProvider turns text into vectors without exposing a provider SDK.
type EmbeddingProvider interface {
	Embed(context.Context, EmbeddingRequest) (EmbeddingResponse, error)
}

// RetrievalRequest is intentionally independent of Qdrant filter syntax.
type RetrievalRequest struct {
	TenantID        string
	KnowledgeBaseID string
	Generation      int64
	Model           EmbeddingModel
	QueryVector     []float32
	Limit           int
	QueryHash       string
}

// RetrievedChunk contains only identifiers and bounded display text.  Final
// authorization and full text loading remain PostgreSQL/application work.
type RetrievedChunk struct {
	ChunkID           string
	ResourceID        string
	DocumentVersionID string
	Score             float64
	Content           string
	QuoteHash         string
}

// KnowledgeRetriever is the retrieval boundary consumed by later RAG work.
type KnowledgeRetriever interface {
	Retrieve(context.Context, RetrievalRequest) ([]RetrievedChunk, error)
}

// AuthorizationRequest is the minimum context needed for a fail-closed ACL
// decision.
type AuthorizationRequest struct {
	TenantID        string
	KnowledgeBaseID string
	UserID          string
	ResourceID      string
	Permission      string
	At              time.Time
}

// AuthorizationDecision contains no cached permission list.
type AuthorizationDecision struct {
	Allowed       bool
	PolicyVersion int64
}

// Authorizer is the application ACL boundary; PostgreSQL remains authoritative.
type Authorizer interface {
	Authorize(context.Context, AuthorizationRequest) (AuthorizationDecision, error)
}

// ProcessingJob is the durable work item exchanged with a worker.
type ProcessingJob struct {
	ID                string
	Type              string
	TenantID          string
	ResourceID        string
	DocumentVersionID string
	IdempotencyKey    string
	Attempt           int
	MaxAttempts       int
	AvailableAt       time.Time
	LeaseExpiresAt    time.Time
}

// JobStore provides claim/lease semantics without committing to a queue vendor.
type JobStore interface {
	Enqueue(context.Context, ProcessingJob) error
	Claim(context.Context, string, time.Duration) (ProcessingJob, bool, error)
	Heartbeat(context.Context, string, string, time.Time) error
	Complete(context.Context, string, string, time.Time) error
	Fail(context.Context, string, string, string, bool, time.Time) error
}

// OutboxEvent is the transactional event envelope.
type OutboxEvent struct {
	ID             string
	TenantID       string
	Type           string
	AggregateType  string
	AggregateID    string
	IdempotencyKey string
	Payload        []byte
	AvailableAt    time.Time
}

// OutboxStore persists events in the same transaction as business changes.
type OutboxStore interface {
	Append(context.Context, OutboxEvent) error
	Claim(context.Context, string, time.Duration) (OutboxEvent, bool, error)
	MarkProcessed(context.Context, string, string, time.Time) error
	MarkFailed(context.Context, string, string, string, bool, time.Time) error
}

var (
	// ErrObjectUnsupported means the source MIME/type is outside the approved policy.
	ErrObjectUnsupported = errors.New("unsupported resource object")
	// ErrParseFailed means parsing failed without exposing source content.
	ErrParseFailed = errors.New("resource parsing failed")
	// ErrEmbeddingFailed means the model provider could not produce a valid batch.
	ErrEmbeddingFailed = errors.New("embedding failed")
	// ErrAuthorizationDenied is a normal fail-closed decision, not a provider error.
	ErrAuthorizationDenied = errors.New("resource authorization denied")
)
