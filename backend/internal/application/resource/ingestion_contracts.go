package resource

import (
	"context"
	"errors"
	"time"
)

const (
	MaxDocumentBytes    = 50 << 20
	IngestionJobIngest  = "ingest"
	IngestionJobPurge   = "purge"
	IngestionJobRebuild = "rebuild"
)

var (
	ErrIngestionInvalid          = errors.New("invalid resource ingestion")
	ErrIngestionConflict         = errors.New("resource ingestion idempotency conflict")
	ErrIngestionUnavailable      = errors.New("resource ingestion unavailable")
	ErrIngestionQueueFull        = errors.New("resource ingestion queue is full")
	ErrIngestionLeaseLost        = errors.New("resource ingestion lease lost")
	ErrIngestionModelUnavailable = errors.New("resource embedding model unavailable")
)

// IngestionRegistration contains server-validated source identity, never a client model selection.
type IngestionRegistration struct {
	Title           string
	Chapter         string
	Topic           string
	KnowledgeBaseID string
	Source          ObjectSource
	Metadata        ObjectMetadata
	IdempotencyKey  string
	ModelVersionID  string
	QueueLimit      int
}

// IngestionStatus is safe to return to the owner; source locators and provider errors stay private.
type IngestionStatus struct {
	JobID             string    `json:"job_id"`
	ResourceID        string    `json:"resource_id"`
	DocumentVersionID string    `json:"document_version_id"`
	KnowledgeBaseID   string    `json:"knowledge_base_id"`
	Title             string    `json:"title"`
	Filename          string    `json:"filename"`
	MIMEType          string    `json:"mime_type"`
	ByteSize          int64     `json:"byte_size"`
	State             string    `json:"state"`
	PublicationStatus string    `json:"publication_status"`
	Stage             string    `json:"stage"`
	Retryable         bool      `json:"retryable"`
	ProcessStatus     string    `json:"process_status"`
	IndexStatus       string    `json:"index_status"`
	JobStatus         string    `json:"job_status"`
	Attempt           int       `json:"attempt"`
	MaxAttempts       int       `json:"max_attempts"`
	ChunkCount        int       `json:"chunk_count"`
	IndexedChunks     int       `json:"indexed_chunks"`
	ErrorCode         string    `json:"error_code,omitempty"`
	CanRetry          bool      `json:"can_retry"`
	CanUnpublish      bool      `json:"can_unpublish"`
	CanDelete         bool      `json:"can_delete"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type IngestionLease struct {
	JobID   string
	Owner   string
	Attempt int
}

type IngestionGeneration struct {
	ID              string
	TenantID        string
	KnowledgeBaseID string
	Number          int64
	Collection      string
	State           string
	Model           EmbeddingModel
	ReconcileCursor string
	RetainUntil     time.Time
}

type IngestionChunk struct {
	ID            string
	ManifestID    string
	ContentSHA256 string
	Draft         ChunkDraft
}

type IngestionManifest struct {
	ID                string
	ChunkID           string
	ResourceID        string
	DocumentVersionID string
	GenerationID      string
	EmbeddingSHA256   string
	ContentSHA256     string
	State             string
	Desired           bool
}

type IngestionWork struct {
	Job        ProcessingJob
	Lease      IngestionLease
	Source     ObjectSource
	Metadata   ObjectMetadata
	Generation IngestionGeneration
	Chunks     []IngestionChunk
}

// ManifestReceipt is supplied only after an acknowledged and verified vector write.
type ManifestReceipt struct {
	ID              string
	EmbeddingSHA256 string
}

type IngestionHistoryCleanup struct {
	Jobs         int64
	OutboxEvents int64
}

type IngestionUploadCleanup struct {
	ID         string
	Source     ObjectSource
	LeaseToken string
}

type IngestionUploadStager interface {
	StageIngestionUpload(ctx context.Context, ownerID string, source ObjectSource, now time.Time) error
}

// IngestionRepository keeps authoritative publication, fencing and outbox changes in one transaction.
type IngestionRepository interface {
	RegisterIngestion(context.Context, string, IngestionRegistration, time.Time) (IngestionStatus, bool, error)
	GetIngestion(context.Context, string, string) (IngestionStatus, bool, error)
	ListIngestions(context.Context, string, int, int) ([]IngestionStatus, int, error)
	ClaimIngestionJob(context.Context, string, time.Time, time.Time) (IngestionWork, bool, error)
	HeartbeatIngestionJob(context.Context, IngestionLease, time.Time, time.Time) (bool, error)
	PrepareIngestionChunks(context.Context, IngestionLease, []ChunkDraft, time.Time) ([]IngestionChunk, error)
	CompleteIngestionJob(context.Context, IngestionLease, []ManifestReceipt, time.Time) (bool, error)
	FailIngestionJob(context.Context, IngestionLease, string, bool, time.Time, time.Time) (bool, error)
	RetryIngestion(context.Context, string, string, time.Time) (IngestionStatus, bool, error)
	WithdrawIngestion(context.Context, string, string, bool, time.Time) (IngestionStatus, bool, error)
	BeginIngestionRebuild(ctx context.Context, knowledgeBaseID, modelVersionID string, now time.Time) (IngestionGeneration, error)
	ListIngestionGenerations(ctx context.Context, afterID string, limit int) ([]IngestionGeneration, error)
	ListIngestionManifests(ctx context.Context, generationID, afterID string, limit int) ([]IngestionManifest, error)
	GetIngestionManifests(ctx context.Context, generationID string, ids []string) ([]IngestionManifest, error)
	ScheduleIngestionRepair(ctx context.Context, generationID, documentVersionID string, now time.Time) (bool, error)
	SaveIngestionReconcileCursor(ctx context.Context, generationID, cursor string) error
}
