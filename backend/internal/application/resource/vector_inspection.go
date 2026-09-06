package resource

import "context"

type VectorScrollRequest struct {
	Route  string
	Filter map[string]any
	Offset string
	Limit  int
}

type VectorScrollPage struct {
	Points     []VectorCandidate
	NextOffset string
}

// VectorInspector verifies durable identities without loading stored vectors.
type VectorInspector interface {
	GetPoints(context.Context, string, []string) ([]VectorCandidate, error)
	ScrollPoints(context.Context, VectorScrollRequest) (VectorScrollPage, error)
	CountPoints(context.Context, string, map[string]any) (int64, error)
}
