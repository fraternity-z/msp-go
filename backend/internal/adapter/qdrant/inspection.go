package qdrant

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	resourceapp "mathstudy/backend/internal/application/resource"
)

var inspectionPayloadFields = []string{"tenant_id", "knowledge_base_id", "resource_id", "document_version_id", "chunk_id", "generation_id", "index_generation", "model_version_id", "visibility", "content_sha256", "embedding_sha256"}

func (c *Client) GetPoints(ctx context.Context, route string, ids []string) ([]resourceapp.VectorCandidate, error) {
	route, err := c.route(route)
	if err != nil {
		return nil, err
	}
	if len(ids) < 1 || len(ids) > 128 {
		return nil, &Error{Operation: "retrieve points", Code: resourceapp.ErrVectorInvalid}
	}
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			return nil, &Error{Operation: "retrieve points", Code: resourceapp.ErrVectorInvalid}
		}
	}
	var response struct {
		Result json.RawMessage `json:"result"`
	}
	if err := c.request(ctx, "retrieve points", http.MethodPost, c.endpoint("collections", route, "points"), nil,
		map[string]any{"ids": ids, "with_payload": inspectionPayloadFields, "with_vector": false}, &response, c.timeout); err != nil {
		return nil, err
	}
	if len(response.Result) == 0 || bytes.Equal(response.Result, []byte("null")) {
		return nil, &Error{Operation: "retrieve points response", Code: resourceapp.ErrVectorUnavailable}
	}
	return decodeCandidates(response.Result)
}

func (c *Client) ScrollPoints(ctx context.Context, input resourceapp.VectorScrollRequest) (resourceapp.VectorScrollPage, error) {
	route, err := c.route(input.Route)
	if err != nil {
		return resourceapp.VectorScrollPage{}, err
	}
	if input.Limit < 1 || input.Limit > 128 || len(input.Filter) == 0 {
		return resourceapp.VectorScrollPage{}, &Error{Operation: "scroll points", Code: resourceapp.ErrVectorInvalid}
	}
	payload := map[string]any{"filter": input.Filter, "limit": input.Limit, "with_payload": inspectionPayloadFields, "with_vector": false}
	if input.Offset != "" {
		if number, err := strconv.ParseUint(input.Offset, 10, 64); err == nil {
			payload["offset"] = number
		} else {
			payload["offset"] = input.Offset
		}
	}
	var response struct {
		Result *struct {
			Points json.RawMessage `json:"points"`
			Next   json.RawMessage `json:"next_page_offset"`
		} `json:"result"`
	}
	if err := c.request(ctx, "scroll points", http.MethodPost, c.endpoint("collections", route, "points", "scroll"), nil, payload, &response, c.timeout); err != nil {
		return resourceapp.VectorScrollPage{}, err
	}
	if response.Result == nil || len(response.Result.Points) == 0 {
		return resourceapp.VectorScrollPage{}, &Error{Operation: "scroll points response", Code: resourceapp.ErrVectorUnavailable}
	}
	points, err := decodeCandidates(response.Result.Points)
	if err != nil || len(points) > input.Limit {
		return resourceapp.VectorScrollPage{}, &Error{Operation: "scroll points response", Code: resourceapp.ErrVectorUnavailable}
	}
	var next string
	if raw := response.Result.Next; len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		next, err = decodePointID(raw)
		if err != nil || next == input.Offset {
			return resourceapp.VectorScrollPage{}, &Error{Operation: "scroll points cursor", Code: resourceapp.ErrVectorUnavailable}
		}
	}
	return resourceapp.VectorScrollPage{Points: points, NextOffset: next}, nil
}

func (c *Client) CountPoints(ctx context.Context, route string, filter map[string]any) (int64, error) {
	route, err := c.route(route)
	if err != nil {
		return 0, err
	}
	if len(filter) == 0 {
		return 0, &Error{Operation: "count points", Code: resourceapp.ErrVectorInvalid}
	}
	var response struct {
		Result *struct {
			Count *int64 `json:"count"`
		} `json:"result"`
	}
	if err := c.request(ctx, "count points", http.MethodPost, c.endpoint("collections", route, "points", "count"), nil,
		map[string]any{"exact": true, "filter": filter}, &response, c.timeout); err != nil {
		return 0, err
	}
	if response.Result == nil || response.Result.Count == nil || *response.Result.Count < 0 {
		return 0, &Error{Operation: "count points response", Code: resourceapp.ErrVectorUnavailable}
	}
	return *response.Result.Count, nil
}
