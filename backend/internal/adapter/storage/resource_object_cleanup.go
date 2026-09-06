package storage

import (
	"context"
	"crypto/hmac"
	"crypto/sha1" // #nosec G505 -- Qiniu management authentication requires HMAC-SHA1.
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	resourceapp "mathstudy/backend/internal/application/resource"
)

var qiniuCleanupHost = regexp.MustCompile(`^rs(?:-[a-z0-9]+(?:-[a-z0-9]+)*)?\.(?:qiniuapi\.com|qbox\.me|qiniu\.com)$`)

// DeleteDocument removes one object after the repository has fenced its unreferenced staging row.
func (m *RuntimeManager) DeleteDocument(ctx context.Context, source resourceapp.ObjectSource) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key, err := documentObjectKey(source.StorageKey)
	if err != nil || !strings.HasPrefix(key, "documents/ingestions/") {
		return resourceapp.ErrObjectUnsupported
	}
	state := m.loadState()
	if state == nil || !privateDocumentBackend(state.backend) {
		return resourceapp.ErrIngestionUnavailable
	}
	switch backend := state.backend.(type) {
	case *LocalStorage:
		return deleteLocalDocument(ctx, backend, source)
	case *S3Storage:
		return deleteS3Document(ctx, backend, source)
	case *QiniuStorage:
		return deleteQiniuDocument(ctx, backend, source)
	default:
		return resourceapp.ErrIngestionUnavailable
	}
}

func deleteLocalDocument(ctx context.Context, backend *LocalStorage, source resourceapp.ObjectSource) error {
	if source.URI != "/uploads/"+source.StorageKey || strings.TrimSpace(backend.uploadDir) == "" {
		return resourceapp.ErrObjectUnsupported
	}
	rootPath, err := filepath.Abs(backend.uploadDir)
	if err != nil {
		return resourceapp.ErrIngestionUnavailable
	}
	root, err := os.OpenRoot(rootPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return resourceapp.ErrIngestionUnavailable
	}
	defer root.Close()
	parts := strings.Split(source.StorageKey, "/")
	for index := 1; index < len(parts); index++ {
		info, err := root.Lstat(filepath.Join(parts[:index]...))
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return resourceapp.ErrIngestionUnavailable
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return resourceapp.ErrObjectUnsupported
		}
	}
	key := filepath.FromSlash(source.StorageKey)
	info, err := root.Lstat(key)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return resourceapp.ErrIngestionUnavailable
	}
	if !info.Mode().IsRegular() {
		return resourceapp.ErrObjectUnsupported
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Root.Remove confines parent traversal and never recursively removes directories.
	if err := root.Remove(key); err != nil && !os.IsNotExist(err) {
		return resourceapp.ErrIngestionUnavailable
	}
	return nil
}

func cleanupSourceMatches(source resourceapp.ObjectSource, base *url.URL) bool {
	u, err := url.Parse(source.URI)
	return err == nil && base != nil && u.User == nil && u.RawQuery == "" && u.Fragment == "" &&
		u.Scheme == base.Scheme && u.Host == base.Host &&
		u.EscapedPath() == strings.TrimRight(base.EscapedPath(), "/")+"/"+awsEncode(source.StorageKey, false)
}

func deleteS3Document(ctx context.Context, backend *S3Storage, source resourceapp.ObjectSource) error {
	base, err := backend.downloadBaseURL()
	if err != nil || !cleanupSourceMatches(source, base) {
		return resourceapp.ErrObjectUnsupported
	}
	deleteCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	objectURL, canonicalURI := backend.objectURL(source.StorageKey)
	request, err := http.NewRequestWithContext(deleteCtx, http.MethodDelete, objectURL, nil)
	if err != nil {
		return resourceapp.ErrIngestionUnavailable
	}
	now := backend.now().UTC()
	payloadHash := sha256Hex(nil)
	headers := map[string]string{"host": backend.endpoint.Host, "x-amz-content-sha256": payloadHash, "x-amz-date": now.Format("20060102T150405Z")}
	request.Header.Set("X-Amz-Content-Sha256", payloadHash)
	request.Header.Set("X-Amz-Date", headers["x-amz-date"])
	request.Header.Set("Authorization", backend.authorization(http.MethodDelete, canonicalURI, "", headers, payloadHash, now))
	response, err := withoutRedirects(backend.client).Do(request)
	if err != nil {
		return cleanupRequestError(deleteCtx)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusNotFound {
		return resourceapp.ErrIngestionUnavailable
	}
	return nil
}

func deleteQiniuDocument(ctx context.Context, backend *QiniuStorage, source resourceapp.ObjectSource) error {
	base, err := url.Parse(backend.cfg.Domain)
	if err != nil || !cleanupSourceMatches(source, base) {
		return resourceapp.ErrObjectUnsupported
	}
	deleteCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	host, err := qiniuDocumentRSHost(deleteCtx, backend)
	if err != nil {
		return err
	}
	entry := base64.URLEncoding.EncodeToString([]byte(backend.cfg.BucketName + ":" + source.StorageKey))
	request, err := http.NewRequestWithContext(deleteCtx, http.MethodPost, "https://"+host+"/delete/"+entry, nil)
	if err != nil {
		return resourceapp.ErrIngestionUnavailable
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("X-Qiniu-Date", backend.now().UTC().Format("20060102T150405Z"))
	// Qiniu V2 signs method, path, host, content type and canonical X-Qiniu headers.
	data := request.Method + " " + request.URL.Path + "\nHost: " + request.Host +
		"\nContent-Type: application/x-www-form-urlencoded\nX-Qiniu-Date: " + request.Header.Get("X-Qiniu-Date") + "\n\n"
	mac := hmac.New(sha1.New, []byte(backend.cfg.SecretKey))
	_, _ = mac.Write([]byte(data))
	request.Header.Set("Authorization", "Qiniu "+backend.cfg.AccessKey+":"+base64.URLEncoding.EncodeToString(mac.Sum(nil)))
	response, err := withoutRedirects(backend.client).Do(request)
	if err != nil {
		return cleanupRequestError(deleteCtx)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != 612 {
		return resourceapp.ErrIngestionUnavailable
	}
	return nil
}

func qiniuDocumentRSHost(ctx context.Context, backend *QiniuStorage) (string, error) {
	query := url.Values{"ak": {backend.cfg.AccessKey}, "bucket": {backend.cfg.BucketName}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://uc.qbox.me/v4/query?"+query.Encode(), nil)
	if err != nil {
		return "", resourceapp.ErrIngestionUnavailable
	}
	response, err := withoutRedirects(backend.client).Do(request)
	if err != nil {
		return "", cleanupRequestError(ctx)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", resourceapp.ErrIngestionUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		return "", resourceapp.ErrIngestionUnavailable
	}
	var result struct {
		Hosts []struct {
			RS struct {
				Domains []string `json:"domains"`
			} `json:"rs"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", resourceapp.ErrIngestionUnavailable
	}
	for _, region := range result.Hosts {
		for _, host := range region.RS.Domains {
			if qiniuCleanupHost.MatchString(host) {
				return host, nil
			}
		}
	}
	return "", resourceapp.ErrIngestionUnavailable
}

func cleanupRequestError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return resourceapp.ErrIngestionUnavailable
}
