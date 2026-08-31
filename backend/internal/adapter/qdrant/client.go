package qdrant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	resourceapp "mathstudy/backend/internal/application/resource"
)

const (
	defaultCollection       = "resource_chunks_dense_default_v1"
	defaultRequestTimeout   = 5 * time.Second
	defaultHealthTimeout    = 3 * time.Second
	defaultMaxBatchSize     = 64
	maxResponseBodyBytes    = 1 << 20
	maxCollectionNameLength = 255
)

var collectionNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,254}$`)

// Config contains only provider connection and transport settings.  Model
// identity and vector dimensions are supplied by the application generation
// contract, not guessed by this adapter.
type Config struct {
	BaseURL        string
	APIKey         string
	Collection     string
	Timeout        time.Duration
	HealthTimeout  time.Duration
	MaxBatchSize   int
	WaitForChanges bool
	PayloadIndexes []string
}

// Option customizes a Client, primarily for deterministic tests.
type Option func(*Client) error

// WithHTTPClient injects an HTTP client without changing request semantics.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(client *Client) error {
		if httpClient == nil {
			return errors.New("qdrant http client is nil")
		}
		client.httpClient = httpClient
		return nil
	}
}

// Client is a small REST adapter for the Qdrant API.
type Client struct {
	baseURL        *url.URL
	apiKey         string
	collection     string
	timeout        time.Duration
	healthTimeout  time.Duration
	maxBatchSize   int
	waitForChanges bool
	payloadIndexes []resourceapp.VectorPayloadIndex
	httpClient     *http.Client

	mu                sync.RWMutex
	expectedDimension int
	expectedDistance  resourceapp.VectorDistance
}

// New validates connection settings and creates a Qdrant client.
func New(cfg Config, options ...Option) (*Client, error) {
	baseURL, err := parseBaseURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	collection := strings.TrimSpace(cfg.Collection)
	if collection == "" {
		collection = defaultCollection
	}
	if !collectionNamePattern.MatchString(collection) {
		return nil, fmt.Errorf("invalid qdrant collection name")
	}
	requestTimeout := cfg.Timeout
	if requestTimeout <= 0 {
		requestTimeout = defaultRequestTimeout
	}
	healthTimeout := cfg.HealthTimeout
	if healthTimeout <= 0 {
		healthTimeout = defaultHealthTimeout
	}
	maxBatchSize := cfg.MaxBatchSize
	if maxBatchSize <= 0 {
		maxBatchSize = defaultMaxBatchSize
	}
	payloadIndexes := make([]resourceapp.VectorPayloadIndex, 0, len(cfg.PayloadIndexes))
	for _, field := range cfg.PayloadIndexes {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if !validPayloadField(field) {
			return nil, errors.New("invalid qdrant payload index field")
		}
		payloadIndexes = append(payloadIndexes, resourceapp.VectorPayloadIndex{Field: field, Kind: "keyword"})
	}
	client := &Client{
		baseURL:        baseURL,
		apiKey:         strings.TrimSpace(cfg.APIKey),
		collection:     collection,
		timeout:        requestTimeout,
		healthTimeout:  healthTimeout,
		maxBatchSize:   maxBatchSize,
		waitForChanges: cfg.WaitForChanges,
		payloadIndexes: payloadIndexes,
		httpClient:     &http.Client{Timeout: requestTimeout},
	}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(client); err != nil {
			return nil, err
		}
	}
	return client, nil
}

func parseBaseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("qdrant url must not be empty")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("qdrant url must include an http or https scheme and host")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("qdrant url scheme must be http or https")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("qdrant url must not contain credentials, query, or fragment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = ""
	return parsed, nil
}

func (c *Client) route(route string) (string, error) {
	route = strings.TrimSpace(route)
	if route == "" {
		return c.collection, nil
	}
	if route != c.collection {
		return "", &Error{Operation: "route", Code: resourceapp.ErrVectorInvalid}
	}
	return route, nil
}

func (c *Client) endpoint(parts ...string) string {
	segments := make([]string, 0, len(parts)+1)
	if c.baseURL.Path != "" {
		segments = append(segments, c.baseURL.Path)
	}
	segments = append(segments, parts...)
	joined := path.Join(segments...)
	if !strings.HasPrefix(joined, "/") {
		joined = "/" + joined
	}
	return joined
}

func (c *Client) request(ctx context.Context, operation, method, endpoint string, query url.Values, input any, output any, timeout time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	target := *c.baseURL
	target.Path = endpoint
	target.RawPath = ""
	target.RawQuery = query.Encode()
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return &Error{Operation: operation, Code: resourceapp.ErrVectorInvalid, Cause: err}
		}
		body = strings.NewReader(string(encoded))
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return &Error{Operation: operation, Code: resourceapp.ErrVectorInvalid, Cause: err}
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("api-key", c.apiKey)
	}
	response, err := c.httpClient.Do(req)
	if err != nil {
		return &Error{Operation: operation, Code: resourceapp.ErrVectorUnavailable, Retryable: true, Cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return httpStatusError(operation, response.StatusCode)
	}
	if output == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxResponseBodyBytes))
	if err := decoder.Decode(output); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return &Error{Operation: operation, Code: resourceapp.ErrVectorUnavailable, Retryable: true, Cause: err}
	}
	return nil
}

func httpStatusError(operation string, status int) *Error {
	code := resourceapp.ErrVectorInvalid
	retryable := false
	switch {
	case status == http.StatusNotFound:
		code = resourceapp.ErrVectorNotFound
	case status == http.StatusTooManyRequests || status >= 500:
		code = resourceapp.ErrVectorUnavailable
		retryable = true
	}
	return &Error{Operation: operation, StatusCode: status, Code: code, Retryable: retryable}
}

// Ping checks the provider health endpoint without exposing response content.
func (c *Client) Ping(ctx context.Context) error {
	return c.request(ctx, "health", http.MethodGet, c.endpoint("healthz"), nil, nil, nil, c.healthTimeout)
}
