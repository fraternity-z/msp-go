package qdrant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	resourceapp "mathstudy/backend/internal/application/resource"
)

type collectionInfoResponse struct {
	Result collectionInfo `json:"result"`
}

type collectionInfo struct {
	Status       string           `json:"status"`
	PointsCount  int64            `json:"points_count"`
	IndexedCount int64            `json:"indexed_vectors_count"`
	Config       collectionConfig `json:"config"`
}

type collectionConfig struct {
	Params collectionParams `json:"params"`
}

type collectionParams struct {
	Vectors json.RawMessage `json:"vectors"`
}

type countResponse struct {
	Result struct {
		Count int64 `json:"count"`
	} `json:"result"`
}

type upsertRequest struct {
	Points []pointWire `json:"points"`
}

type pointWire struct {
	ID      string         `json:"id"`
	Vector  []float32      `json:"vector"`
	Payload map[string]any `json:"payload,omitempty"`
}

type deletePointsRequest struct {
	Points []string       `json:"points,omitempty"`
	Filter map[string]any `json:"filter,omitempty"`
}

type searchRequest struct {
	Vector      []float32      `json:"vector,omitempty"`
	Query       []float32      `json:"query,omitempty"`
	Limit       int            `json:"limit"`
	Filter      map[string]any `json:"filter,omitempty"`
	WithPayload any            `json:"with_payload,omitempty"`
}

type searchResponse struct {
	Result json.RawMessage `json:"result"`
}

type pointResult struct {
	ID      json.RawMessage `json:"id"`
	Score   float64         `json:"score"`
	Payload map[string]any  `json:"payload"`
}

// EnsureCollection creates a missing collection and verifies an existing
// collection without ever changing its dimension or distance metric.
func (c *Client) EnsureCollection(ctx context.Context, spec resourceapp.VectorCollectionSpec) error {
	route, err := c.route(spec.Route)
	if err != nil {
		return err
	}
	if err := validateCollectionSpec(spec); err != nil {
		return err
	}
	info, err := c.getCollection(ctx, route)
	if err != nil {
		var providerErr *Error
		if !errors.As(err, &providerErr) || providerErr.StatusCode != http.StatusNotFound {
			return err
		}
		if err := c.createCollection(ctx, route, spec); err != nil {
			var conflict *Error
			if !errors.As(err, &conflict) || conflict.StatusCode != http.StatusConflict {
				return err
			}
		}
		info, err = c.getCollection(ctx, route)
		if err != nil {
			return err
		}
	}
	if err := verifyCollectionInfo(route, info, spec); err != nil {
		return err
	}
	c.setExpectedSchema(spec.Dimension, spec.Distance)
	indexes := spec.PayloadIndexes
	if len(indexes) == 0 {
		indexes = c.payloadIndexes
	}
	for _, index := range indexes {
		if err := c.ensurePayloadIndex(ctx, route, index); err != nil {
			return err
		}
	}
	return nil
}

// VerifyCollection reads and validates the collection shape and point count.
func (c *Client) VerifyCollection(ctx context.Context, spec resourceapp.VectorCollectionSpec) (resourceapp.VectorCollectionStatus, error) {
	route, err := c.route(spec.Route)
	if err != nil {
		return resourceapp.VectorCollectionStatus{}, err
	}
	if err := validateCollectionSpec(spec); err != nil {
		return resourceapp.VectorCollectionStatus{}, err
	}
	info, err := c.getCollection(ctx, route)
	if err != nil {
		return resourceapp.VectorCollectionStatus{}, err
	}
	if err := verifyCollectionInfo(route, info, spec); err != nil {
		return resourceapp.VectorCollectionStatus{}, err
	}
	var count countResponse
	if err := c.request(ctx, "count", http.MethodPost, c.endpoint("collections", route, "points", "count"), nil, map[string]any{"exact": true}, &count, c.timeout); err != nil {
		return resourceapp.VectorCollectionStatus{}, err
	}
	c.setExpectedSchema(spec.Dimension, spec.Distance)
	return resourceapp.VectorCollectionStatus{
		Route:      route,
		Dimension:  spec.Dimension,
		Distance:   spec.Distance,
		PointCount: count.Result.Count,
	}, nil
}

func (c *Client) getCollection(ctx context.Context, route string) (collectionInfo, error) {
	var response collectionInfoResponse
	if err := c.request(ctx, "get collection", http.MethodGet, c.endpoint("collections", route), nil, nil, &response, c.timeout); err != nil {
		return collectionInfo{}, err
	}
	if len(response.Result.Config.Params.Vectors) == 0 {
		return collectionInfo{}, &Error{Operation: "get collection", Code: resourceapp.ErrVectorInvalid}
	}
	return response.Result, nil
}

func (c *Client) createCollection(ctx context.Context, route string, spec resourceapp.VectorCollectionSpec) error {
	payload := map[string]any{
		"vectors": map[string]any{
			"size":     spec.Dimension,
			"distance": qdrantDistance(spec.Distance),
		},
	}
	return c.request(ctx, "create collection", http.MethodPut, c.endpoint("collections", route), nil, payload, nil, c.timeout)
}

func (c *Client) ensurePayloadIndex(ctx context.Context, route string, index resourceapp.VectorPayloadIndex) error {
	field := strings.TrimSpace(index.Field)
	if !validPayloadField(field) {
		return &Error{Operation: "payload index", Code: resourceapp.ErrVectorInvalid}
	}
	kind := strings.ToLower(strings.TrimSpace(index.Kind))
	if kind == "" {
		kind = "keyword"
	}
	switch kind {
	case "keyword", "integer", "float", "bool", "text":
	default:
		return &Error{Operation: "payload index", Code: resourceapp.ErrVectorInvalid}
	}
	payload := map[string]any{"field_schema": kind}
	err := c.request(ctx, "payload index", http.MethodPut, c.endpoint("collections", route, "index", field), nil, payload, nil, c.timeout)
	return err
}

func validPayloadField(field string) bool {
	field = strings.TrimSpace(field)
	if field == "" || field == "." || field == ".." || len(field) > maxCollectionNameLength || strings.ContainsAny(field, "/\r\n") {
		return false
	}
	for _, char := range field {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '.' && char != '_' && char != '-' {
			return false
		}
	}
	return true
}

// Upsert writes deterministic point IDs and waits for the provider when the
// client or request asks for wait-for-changes semantics.
func (c *Client) Upsert(ctx context.Context, batch resourceapp.VectorBatch) error {
	route, err := c.route(batch.Route)
	if err != nil {
		return err
	}
	if len(batch.Points) == 0 || len(batch.Points) > c.maxBatchSize {
		return &Error{Operation: "upsert", Code: resourceapp.ErrVectorInvalid}
	}
	dimension := c.expectedVectorDimension()
	points := make([]pointWire, 0, len(batch.Points))
	for _, point := range batch.Points {
		if strings.TrimSpace(point.ID) == "" || len(point.Values) == 0 || !validVector(point.Values) {
			return &Error{Operation: "upsert", Code: resourceapp.ErrVectorInvalid}
		}
		if dimension > 0 && len(point.Values) != dimension {
			return &Error{Operation: "upsert", Code: resourceapp.ErrVectorInvalid}
		}
		points = append(points, pointWire{ID: point.ID, Vector: point.Values, Payload: point.Payload})
	}
	query := waitQuery(c.waitForChanges || batch.Wait)
	return c.request(ctx, "upsert", http.MethodPut, c.endpoint("collections", route, "points"), query, upsertRequest{Points: points}, nil, c.timeout)
}

// Delete removes points by IDs or a provider-neutral filter, never both.
func (c *Client) Delete(ctx context.Context, request resourceapp.VectorDeleteRequest) error {
	route, err := c.route(request.Route)
	if err != nil {
		return err
	}
	if (len(request.IDs) == 0) == (len(request.Filter) == 0) {
		return &Error{Operation: "delete", Code: resourceapp.ErrVectorInvalid}
	}
	ids := make([]string, len(request.IDs))
	copy(ids, request.IDs)
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			return &Error{Operation: "delete", Code: resourceapp.ErrVectorInvalid}
		}
	}
	query := waitQuery(c.waitForChanges || request.Wait)
	payload := deletePointsRequest{Points: ids, Filter: request.Filter}
	return c.request(ctx, "delete", http.MethodPost, c.endpoint("collections", route, "points", "delete"), query, payload, nil, c.timeout)
}

// Search queries the modern Qdrant points/query endpoint and falls back to the
// legacy points/search endpoint for older development images.
func (c *Client) Search(ctx context.Context, request resourceapp.VectorSearchRequest) ([]resourceapp.VectorCandidate, error) {
	route, err := c.route(request.Route)
	if err != nil {
		return nil, err
	}
	if len(request.Values) == 0 || !validVector(request.Values) {
		return nil, &Error{Operation: "search", Code: resourceapp.ErrVectorInvalid}
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 1000 {
		return nil, &Error{Operation: "search", Code: resourceapp.ErrVectorInvalid}
	}
	if dimension := c.expectedVectorDimension(); dimension > 0 && len(request.Values) != dimension {
		return nil, &Error{Operation: "search", Code: resourceapp.ErrVectorInvalid}
	}
	withPayload := any(request.WithPayload)
	if len(request.PayloadFields) > 0 {
		withPayload = request.PayloadFields
	}
	modern := searchRequest{Query: request.Values, Limit: limit, Filter: request.Filter, WithPayload: withPayload}
	var response searchResponse
	err = c.request(ctx, "search", http.MethodPost, c.endpoint("collections", route, "points", "query"), nil, modern, &response, c.timeout)
	if err != nil {
		var providerErr *Error
		if !errors.As(err, &providerErr) || (providerErr.StatusCode != http.StatusNotFound && providerErr.StatusCode != http.StatusMethodNotAllowed) {
			return nil, err
		}
		legacy := searchRequest{Vector: request.Values, Limit: limit, Filter: request.Filter, WithPayload: withPayload}
		if err := c.request(ctx, "search", http.MethodPost, c.endpoint("collections", route, "points", "search"), nil, legacy, &response, c.timeout); err != nil {
			return nil, err
		}
	}
	return decodeCandidates(response.Result)
}

func decodeCandidates(raw json.RawMessage) ([]resourceapp.VectorCandidate, error) {
	var points []pointResult
	if err := json.Unmarshal(raw, &points); err != nil {
		var wrapped struct {
			Points []pointResult `json:"points"`
		}
		if wrappedErr := json.Unmarshal(raw, &wrapped); wrappedErr != nil {
			return nil, &Error{Operation: "search response", Code: resourceapp.ErrVectorUnavailable, Cause: err}
		}
		points = wrapped.Points
	}
	result := make([]resourceapp.VectorCandidate, 0, len(points))
	for _, point := range points {
		id, err := decodePointID(point.ID)
		if err != nil {
			return nil, &Error{Operation: "search response", Code: resourceapp.ErrVectorUnavailable, Cause: err}
		}
		result = append(result, resourceapp.VectorCandidate{ID: id, Score: point.Score, Payload: point.Payload})
	}
	return result, nil
}

func decodePointID(raw json.RawMessage) (string, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if strings.TrimSpace(text) == "" {
			return "", errors.New("empty point id")
		}
		return text, nil
	}
	var number json.Number
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil {
		return "", err
	}
	return number.String(), nil
}

func validateCollectionSpec(spec resourceapp.VectorCollectionSpec) error {
	if spec.Dimension <= 0 || !validDistance(spec.Distance) {
		return &Error{Operation: "collection schema", Code: resourceapp.ErrVectorInvalid}
	}
	if spec.Route != "" && !collectionNamePattern.MatchString(spec.Route) {
		return &Error{Operation: "collection schema", Code: resourceapp.ErrVectorInvalid}
	}
	return nil
}

func verifyCollectionInfo(route string, info collectionInfo, spec resourceapp.VectorCollectionSpec) error {
	dimension, distance, err := parseVectorConfig(info.Config.Params.Vectors)
	if err != nil {
		return &Error{Operation: "collection schema", Code: resourceapp.ErrVectorInvalid, Detail: "provider returned an unsupported vector configuration", Cause: err}
	}
	if dimension != spec.Dimension || distance != spec.Distance {
		return &Error{
			Operation: "collection schema",
			Code:      resourceapp.ErrVectorInvalid,
			Detail:    fmt.Sprintf("expected dimension=%d distance=%s, got dimension=%d distance=%s", spec.Dimension, spec.Distance, dimension, distance),
		}
	}
	return nil
}

func parseVectorConfig(raw json.RawMessage) (int, resourceapp.VectorDistance, error) {
	var config struct {
		Size     int    `json:"size"`
		Distance string `json:"distance"`
	}
	if err := json.Unmarshal(raw, &config); err != nil || config.Size <= 0 {
		return 0, "", errors.New("qdrant collection uses unsupported named vectors")
	}
	distance, err := parseQdrantDistance(config.Distance)
	if err != nil {
		return 0, "", err
	}
	return config.Size, distance, nil
}

func qdrantDistance(distance resourceapp.VectorDistance) string {
	switch distance {
	case resourceapp.VectorDistanceDot:
		return "Dot"
	case resourceapp.VectorDistanceEuclid:
		return "Euclid"
	default:
		return "Cosine"
	}
}

func parseQdrantDistance(value string) (resourceapp.VectorDistance, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "cosine":
		return resourceapp.VectorDistanceCosine, nil
	case "dot", "ip", "innerproduct":
		return resourceapp.VectorDistanceDot, nil
	case "euclid", "l2":
		return resourceapp.VectorDistanceEuclid, nil
	default:
		return "", fmt.Errorf("unsupported qdrant distance")
	}
}

func validDistance(distance resourceapp.VectorDistance) bool {
	switch distance {
	case resourceapp.VectorDistanceCosine, resourceapp.VectorDistanceDot, resourceapp.VectorDistanceEuclid:
		return true
	default:
		return false
	}
}

func validVector(values []float32) bool {
	for _, value := range values {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return false
		}
	}
	return true
}

func waitQuery(wait bool) url.Values {
	if !wait {
		return nil
	}
	return url.Values{"wait": []string{strconv.FormatBool(true)}}
}

func (c *Client) setExpectedSchema(dimension int, distance resourceapp.VectorDistance) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expectedDimension = dimension
	c.expectedDistance = distance
}

func (c *Client) expectedVectorDimension() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.expectedDimension
}
