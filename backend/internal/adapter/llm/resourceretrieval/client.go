package resourceretrieval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"mathstudy/backend/internal/platform/httpjson"
	"mathstudy/backend/internal/platform/outbound"
)

const maxProviderResponseBytes = 4 << 20

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

func selectHTTPClient(clients []HTTPDoer) (HTTPDoer, error) {
	if len(clients) > 1 {
		return nil, errors.New("resource retrieval accepts at most one HTTP client")
	}
	if len(clients) == 1 && clients[0] != nil {
		return clients[0], nil
	}
	return outbound.NewPublicHTTPSClient(10 * time.Second), nil
}

func providerJSON(ctx context.Context, client HTTPDoer, baseURL, apiKey, path string, payload any, target any) (bool, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return false, errors.New("invalid resource model request")
	}
	endpoint := strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(endpoint, "/v1") {
		endpoint += "/v1"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+path, bytes.NewReader(body))
	if err != nil {
		return false, errors.New("invalid resource model endpoint")
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return true, errors.New("resource model transport unavailable")
	}
	if response == nil || response.Body == nil {
		return true, errors.New("resource model response unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return response.StatusCode == 408 || response.StatusCode == 429 || response.StatusCode >= 500,
			errors.New("resource model request rejected")
	}
	if err := httpjson.DecodeLimited(response.Body, maxProviderResponseBytes, target); err != nil {
		return false, errors.New("invalid resource model response")
	}
	return false, nil
}

func waitForRetry(ctx context.Context, attempt int) error {
	timer := time.NewTimer(time.Duration(1<<min(attempt, 3)) * 100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func boundaryError(ctx context.Context, fallback error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fallback
}
