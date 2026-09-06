package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	resourceapp "mathstudy/backend/internal/application/resource"
)

const maxDocumentObjectBytes = resourceapp.MaxDocumentBytes

// StoreDocument stores a server-named document without persisting signed URLs.
func (m *RuntimeManager) StoreDocument(ctx context.Context, reader io.Reader, key, contentType string, size int64) (resourceapp.ObjectSource, error) {
	return m.StoreDocumentWithStaging(ctx, reader, key, contentType, size, nil)
}

// StoreDocumentWithStaging binds the durable upload reservation to the same
// storage snapshot used for writing, including during administrator changes.
func (m *RuntimeManager) StoreDocumentWithStaging(ctx context.Context, reader io.Reader, key, contentType string, size int64, stage func(resourceapp.ObjectSource) error) (resourceapp.ObjectSource, error) {
	state := m.loadState()
	if state == nil || !privateDocumentBackend(state.backend) || size <= 0 || size > maxDocumentObjectBytes {
		return resourceapp.ObjectSource{}, errors.New("document storage is unavailable or not private")
	}
	if _, err := documentObjectKey(key); err != nil {
		return resourceapp.ObjectSource{}, err
	}
	planned, err := documentUploadSource(state.backend, key)
	if err != nil {
		return resourceapp.ObjectSource{}, err
	}
	if stage != nil {
		if err := stage(planned); err != nil {
			return resourceapp.ObjectSource{}, err
		}
	}
	stored, err := state.backend.UploadStream(ctx, &documentContextReader{ctx: ctx, reader: reader}, key, contentType, size)
	if err != nil || stored.Size != size || ctx.Err() != nil {
		return resourceapp.ObjectSource{}, errors.New("document upload failed")
	}
	u, err := url.Parse(stored.URL)
	if err != nil || u.User != nil || stored.Key != key {
		return resourceapp.ObjectSource{}, errors.New("document storage returned an invalid identity")
	}
	u.RawQuery, u.Fragment = "", ""
	if u.String() != planned.URI {
		return resourceapp.ObjectSource{}, errors.New("document storage identity changed")
	}
	return planned, nil
}

func documentUploadSource(backend UploadBackend, key string) (resourceapp.ObjectSource, error) {
	var locator string
	switch storage := backend.(type) {
	case *LocalStorage:
		locator = "/uploads/" + key
	case *S3Storage:
		locator = storage.downloadURL(key)
	case *QiniuStorage:
		locator = storage.downloadURL(key)
	default:
		return resourceapp.ObjectSource{}, resourceapp.ErrObjectUnsupported
	}
	u, err := url.Parse(locator)
	if err != nil || u.User != nil {
		return resourceapp.ObjectSource{}, resourceapp.ErrObjectUnsupported
	}
	u.RawQuery, u.Fragment = "", ""
	return resourceapp.ObjectSource{URI: u.String(), StorageKey: key}, nil
}

func privateDocumentBackend(backend UploadBackend) bool {
	switch value := backend.(type) {
	case *LocalStorage:
		return value != nil
	case *S3Storage:
		return value != nil && value.cfg.PrivateBucket
	case *QiniuStorage:
		return value != nil && value.cfg.PrivateBucket
	default:
		return false
	}
}

// Open reads only within the currently selected private storage namespace.
func (m *RuntimeManager) Open(ctx context.Context, source resourceapp.ObjectSource) (io.ReadCloser, resourceapp.ObjectMetadata, error) {
	state := m.loadState()
	if state == nil || !privateDocumentBackend(state.backend) {
		return nil, resourceapp.ObjectMetadata{}, errors.New("document storage is unavailable or not private")
	}
	reader, ok := state.backend.(resourceapp.ObjectReader)
	if !ok {
		return nil, resourceapp.ObjectMetadata{}, errors.New("document reader is unavailable")
	}
	return reader.Open(ctx, source)
}

func (s *LocalStorage) Open(ctx context.Context, source resourceapp.ObjectSource) (io.ReadCloser, resourceapp.ObjectMetadata, error) {
	key, err := documentObjectKey(source.StorageKey)
	if err != nil || source.URI != "/uploads/"+key || s == nil || s.uploadDir == "" {
		return nil, resourceapp.ObjectMetadata{}, resourceapp.ErrObjectUnsupported
	}
	if err := ctx.Err(); err != nil {
		return nil, resourceapp.ObjectMetadata{}, err
	}
	root, err := filepath.Abs(s.uploadDir)
	if err != nil {
		return nil, resourceapp.ObjectMetadata{}, errors.New("document root is unavailable")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, resourceapp.ObjectMetadata{}, errors.New("document root is unavailable")
	}
	target, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(key)))
	if err != nil || !isSubpath(root, target) {
		return nil, resourceapp.ObjectMetadata{}, errors.New("document object is unavailable")
	}
	// #nosec G304 -- canonical key and symlink-resolved containment constrain this path.
	file, err := os.Open(target)
	if err != nil {
		return nil, resourceapp.ObjectMetadata{}, errors.New("document object is unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxDocumentObjectBytes {
		return nil, resourceapp.ObjectMetadata{}, resourceapp.ErrObjectUnsupported
	}
	return readDocumentObject(ctx, file, key, "")
}

func (s *S3Storage) Open(ctx context.Context, source resourceapp.ObjectSource) (io.ReadCloser, resourceapp.ObjectMetadata, error) {
	if s == nil || !s.cfg.PrivateBucket {
		return nil, resourceapp.ObjectMetadata{}, resourceapp.ErrObjectUnsupported
	}
	base, err := s.downloadBaseURL()
	if err != nil {
		return nil, resourceapp.ObjectMetadata{}, resourceapp.ErrObjectUnsupported
	}
	return openRemoteDocument(ctx, source, base, s.downloadURL, s.client)
}

func (s *QiniuStorage) Open(ctx context.Context, source resourceapp.ObjectSource) (io.ReadCloser, resourceapp.ObjectMetadata, error) {
	if s == nil || !s.cfg.PrivateBucket {
		return nil, resourceapp.ObjectMetadata{}, resourceapp.ErrObjectUnsupported
	}
	base, err := url.Parse(strings.TrimRight(s.cfg.Domain, "/"))
	if err != nil {
		return nil, resourceapp.ObjectMetadata{}, resourceapp.ErrObjectUnsupported
	}
	return openRemoteDocument(ctx, source, base, s.downloadURL, s.client)
}

func openRemoteDocument(ctx context.Context, source resourceapp.ObjectSource, base *url.URL, signedURL func(string) string, client *http.Client) (io.ReadCloser, resourceapp.ObjectMetadata, error) {
	key, err := documentObjectKey(source.StorageKey)
	u, parseErr := url.Parse(source.URI)
	if err != nil || parseErr != nil || base == nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		u.Scheme != base.Scheme || u.Host != base.Host || u.EscapedPath() != strings.TrimRight(base.EscapedPath(), "/")+"/"+awsEncode(key, false) {
		return nil, resourceapp.ObjectMetadata{}, resourceapp.ErrObjectUnsupported
	}
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(readCtx, http.MethodGet, signedURL(key), nil)
	if err != nil {
		return nil, resourceapp.ObjectMetadata{}, errors.New("document request failed")
	}
	response, err := withoutRedirects(client).Do(request)
	if err != nil {
		return nil, resourceapp.ObjectMetadata{}, errors.New("document download failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > maxDocumentObjectBytes {
		return nil, resourceapp.ObjectMetadata{}, errors.New("document download failed")
	}
	return readDocumentObject(readCtx, response.Body, key, response.Header.Get("Content-Type"))
}

func documentObjectKey(key string) (string, error) {
	clean, err := cleanObjectKey(key)
	if err != nil || clean != key || !strings.HasPrefix(key, "documents/") || documentMIME(key) == "" {
		return "", resourceapp.ErrObjectUnsupported
	}
	return clean, nil
}

func documentMIME(filename string) string {
	switch strings.ToLower(path.Ext(filename)) {
	case ".pdf":
		return "application/pdf"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".txt":
		return "text/plain"
	case ".md":
		return "text/markdown"
	default:
		return ""
	}
}

func readDocumentObject(ctx context.Context, reader io.Reader, key, declaredType string) (io.ReadCloser, resourceapp.ObjectMetadata, error) {
	expectedType := documentMIME(key)
	if declaredType != "" {
		contentType, _, err := mime.ParseMediaType(declaredType)
		if err != nil || (contentType != expectedType && contentType != "application/octet-stream" && !(expectedType == "text/markdown" && contentType == "text/plain")) {
			return nil, resourceapp.ObjectMetadata{}, resourceapp.ErrObjectUnsupported
		}
	}
	data, err := io.ReadAll(io.LimitReader(&documentContextReader{ctx: ctx, reader: reader}, maxDocumentObjectBytes+1))
	if err != nil || ctx.Err() != nil {
		return nil, resourceapp.ObjectMetadata{}, errors.New("document read failed")
	}
	if len(data) == 0 || len(data) > maxDocumentObjectBytes {
		return nil, resourceapp.ObjectMetadata{}, resourceapp.ErrObjectUnsupported
	}
	sum := sha256.Sum256(data)
	metadata := resourceapp.ObjectMetadata{Filename: path.Base(key), MIMEType: expectedType, ByteSize: int64(len(data)), Checksum: hex.EncodeToString(sum[:])}
	return io.NopCloser(bytes.NewReader(data)), metadata, nil
}

type documentContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *documentContextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}
