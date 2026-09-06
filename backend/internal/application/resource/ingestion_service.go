package resource

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"path"
	"strings"
	"time"
	"unicode/utf8"
)

type IngestionModelProvider interface {
	CurrentModel(context.Context) (EmbeddingModel, int, error)
}

type DocumentObjectStore interface {
	ObjectReader
	StoreDocument(context.Context, io.Reader, string, string, int64) (ObjectSource, error)
}

type ingestionUploadStager interface {
	StageIngestionUpload(context.Context, string, ObjectSource, time.Time) error
}

type stagedDocumentObjectStore interface {
	StoreDocumentWithStaging(context.Context, io.Reader, string, string, int64, func(ObjectSource) error) (ObjectSource, error)
}

type DocumentUpload struct {
	Title           string
	Chapter         string
	Topic           string
	ClientRequestID string
	Filename        string
	MIMEType        string
	ByteSize        int64
	Reader          io.Reader
}

type IngestionList struct {
	Items    []IngestionStatus `json:"items"`
	Total    int               `json:"total"`
	Page     int               `json:"page"`
	PageSize int               `json:"page_size"`
	HasMore  bool              `json:"has_more"`
}

type IngestionService struct {
	repo    IngestionRepository
	objects DocumentObjectStore
	models  IngestionModelProvider
	now     func() time.Time
}

func NewIngestionService(repo IngestionRepository, objects DocumentObjectStore, models IngestionModelProvider) (*IngestionService, error) {
	if repo == nil || objects == nil || models == nil {
		return nil, errors.New("resource ingestion dependencies are incomplete")
	}
	_, durableStaging := repo.(ingestionUploadStager)
	_, stagedStorage := objects.(stagedDocumentObjectStore)
	if durableStaging != stagedStorage {
		return nil, errors.New("resource upload staging dependencies are incomplete")
	}
	return &IngestionService{repo: repo, objects: objects, models: models, now: func() time.Time { return time.Now().UTC() }}, nil
}

// Upload registers one immutable document; transport retries reuse its request ID.
func (s *IngestionService) Upload(ctx context.Context, ownerID string, input DocumentUpload) (IngestionStatus, error) {
	input.Title, input.Chapter, input.Topic = strings.TrimSpace(input.Title), strings.TrimSpace(input.Chapter), strings.TrimSpace(input.Topic)
	input.Filename = path.Base(strings.ReplaceAll(input.Filename, "\\", "/"))
	if !isSearchUUID(ownerID) || !isSearchUUID(input.ClientRequestID) || input.Reader == nil || input.ByteSize <= 0 || input.ByteSize > MaxDocumentBytes ||
		!validIngestionText(input.Title, 500, false) || !validIngestionText(input.Chapter, 100, true) || !validIngestionText(input.Topic, 100, true) ||
		!validIngestionText(input.Filename, 255, false) {
		return IngestionStatus{}, ErrIngestionInvalid
	}
	model, _, err := s.models.CurrentModel(ctx)
	if err != nil {
		return IngestionStatus{}, ingestionBoundaryError(ctx, ErrIngestionModelUnavailable)
	}
	if !isSearchUUID(model.ID) {
		return IngestionStatus{}, ErrIngestionModelUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(&ingestionContextReader{ctx: ctx, reader: input.Reader}, MaxDocumentBytes+1))
	if err != nil || ctx.Err() != nil {
		return IngestionStatus{}, ingestionBoundaryError(ctx, ErrIngestionInvalid)
	}
	if int64(len(data)) != input.ByteSize || len(data) == 0 || len(data) > MaxDocumentBytes {
		return IngestionStatus{}, ErrIngestionInvalid
	}
	contentType, err := validateDocumentUpload(input.Filename, input.MIMEType, data)
	if err != nil {
		return IngestionStatus{}, err
	}
	sum := sha256.Sum256(data)
	checksum := hex.EncodeToString(sum[:])
	key := "documents/ingestions/" + strings.ToLower(ownerID) + "/" + strings.ToLower(input.ClientRequestID) + "/" + checksum + strings.ToLower(path.Ext(input.Filename))
	var source ObjectSource
	if staged, ok := s.objects.(stagedDocumentObjectStore); ok {
		var stageError error
		var stagedSource ObjectSource
		stageCalled := false
		source, err = staged.StoreDocumentWithStaging(ctx, bytes.NewReader(data), key, contentType, input.ByteSize, func(source ObjectSource) error {
			if stageCalled {
				stageError = ErrIngestionUnavailable
				return stageError
			}
			stageCalled, stagedSource = true, source
			stageError = s.repo.(ingestionUploadStager).StageIngestionUpload(ctx, ownerID, source, s.now())
			return stageError
		})
		if stageError != nil {
			return IngestionStatus{}, stageError
		}
		if !stageCalled || source != stagedSource {
			return IngestionStatus{}, ingestionBoundaryError(ctx, ErrIngestionUnavailable)
		}
	} else {
		source, err = s.objects.StoreDocument(ctx, bytes.NewReader(data), key, contentType, input.ByteSize)
	}
	if err != nil {
		return IngestionStatus{}, ingestionBoundaryError(ctx, ErrIngestionUnavailable)
	}
	result, _, err := s.repo.RegisterIngestion(ctx, ownerID, IngestionRegistration{
		Title: input.Title, Chapter: input.Chapter, Topic: input.Topic,
		KnowledgeBaseID: DefaultSearchKnowledgeBaseID,
		Source:          source, Metadata: ObjectMetadata{Filename: input.Filename, MIMEType: contentType, ByteSize: input.ByteSize, Checksum: checksum},
		IdempotencyKey: strings.ToLower(input.ClientRequestID), ModelVersionID: model.ID, QueueLimit: 1000,
	}, s.now())
	return result, err
}

func (s *IngestionService) Get(ctx context.Context, ownerID, resourceID string) (IngestionStatus, error) {
	if !isSearchUUID(ownerID) || !isSearchUUID(resourceID) {
		return IngestionStatus{}, ErrIngestionInvalid
	}
	item, ok, err := s.repo.GetIngestion(ctx, ownerID, resourceID)
	if err == nil && !ok {
		err = ErrNotFound
	}
	return item, err
}

func (s *IngestionService) List(ctx context.Context, ownerID string, page, size int) (IngestionList, error) {
	if !isSearchUUID(ownerID) || page < 1 || page > 10000 || size < 1 || size > 100 {
		return IngestionList{}, ErrIngestionInvalid
	}
	items, total, err := s.repo.ListIngestions(ctx, ownerID, size, (page-1)*size)
	if items == nil {
		items = []IngestionStatus{}
	}
	return IngestionList{Items: items, Total: total, Page: page, PageSize: size, HasMore: page*size < total}, err
}

func (s *IngestionService) Retry(ctx context.Context, ownerID, resourceID string) (IngestionStatus, error) {
	if !isSearchUUID(ownerID) || !isSearchUUID(resourceID) {
		return IngestionStatus{}, ErrIngestionInvalid
	}
	if _, _, err := s.models.CurrentModel(ctx); err != nil {
		return IngestionStatus{}, ingestionBoundaryError(ctx, ErrIngestionModelUnavailable)
	}
	item, ok, err := s.repo.RetryIngestion(ctx, ownerID, resourceID, s.now())
	if err == nil && !ok {
		err = ErrNotFound
	}
	return item, err
}

func (s *IngestionService) Withdraw(ctx context.Context, ownerID, resourceID string, remove bool) (IngestionStatus, error) {
	if !isSearchUUID(ownerID) || !isSearchUUID(resourceID) {
		return IngestionStatus{}, ErrIngestionInvalid
	}
	item, ok, err := s.repo.WithdrawIngestion(ctx, ownerID, resourceID, remove, s.now())
	if err == nil && !ok {
		err = ErrNotFound
	}
	return item, err
}

func validIngestionText(value string, limit int, empty bool) bool {
	return (empty || value != "") && utf8.ValidString(value) && utf8.RuneCountInString(value) <= limit && !strings.ContainsAny(value, "\x00\r\n")
}

func validateDocumentUpload(filename, declared string, data []byte) (string, error) {
	var expected string
	switch strings.ToLower(path.Ext(filename)) {
	case ".pdf":
		expected = "application/pdf"
		if !bytes.HasPrefix(data, []byte("%PDF-")) {
			return "", ErrObjectUnsupported
		}
	case ".docx":
		expected = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
		if !bytes.HasPrefix(data, []byte{'P', 'K', 3, 4}) {
			return "", ErrObjectUnsupported
		}
	case ".txt", ".md":
		expected = "text/plain"
		if strings.EqualFold(path.Ext(filename), ".md") {
			expected = "text/markdown"
		}
		if !utf8.Valid(data) || bytes.ContainsRune(data, 0) {
			return "", ErrObjectUnsupported
		}
	default:
		return "", ErrObjectUnsupported
	}
	if declared != "" {
		parsed, _, err := mime.ParseMediaType(declared)
		if err != nil || (parsed != expected && parsed != "application/octet-stream" && !(expected == "text/markdown" && parsed == "text/plain")) {
			return "", ErrObjectUnsupported
		}
	}
	return expected, nil
}

func ingestionBoundaryError(ctx context.Context, fallback error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fallback
}

type ingestionContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *ingestionContextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}
