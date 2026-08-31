package qdrant

import (
	"errors"
	"fmt"
)

// Error is a sanitized Qdrant adapter error.  Response bodies are deliberately
// excluded so provider payloads and credentials cannot leak into logs.
type Error struct {
	Operation  string
	Code       error
	StatusCode int
	Retryable  bool
	Detail     string
	Cause      error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("qdrant %s failed (status %d)", e.Operation, e.StatusCode)
	}
	if e.Code != nil {
		if e.Detail != "" {
			return fmt.Sprintf("qdrant %s failed: %s (%s)", e.Operation, e.Code.Error(), e.Detail)
		}
		return fmt.Sprintf("qdrant %s failed: %s", e.Operation, e.Code.Error())
	}
	return fmt.Sprintf("qdrant %s failed", e.Operation)
}

// Unwrap keeps the original transport error available to callers.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Is lets callers classify the adapter error with resource.ErrVector* while
// preserving the transport cause through Unwrap.
func (e *Error) Is(target error) bool {
	if e == nil {
		return false
	}
	if e.Code != nil && (target == e.Code || errors.Is(e.Code, target)) {
		return true
	}
	return e.Cause != nil && errors.Is(e.Cause, target)
}
