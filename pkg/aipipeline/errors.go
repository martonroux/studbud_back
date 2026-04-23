package aipipeline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"studbud/backend/internal/myErrors"
)

// classifyProviderStartErr wraps raw provider-client errors into sentinel AppErrors.
// Called for synchronous errors returned from aiProvider.Client.Stream.
func classifyProviderStartErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, myErrors.ErrContentPolicy) {
		return err
	}
	if errors.Is(err, myErrors.ErrAIProvider) {
		return err
	}
	return &myErrors.AppError{Code: "provider_5xx", Message: "AI service failed before streaming", Wrapped: myErrors.ErrAIProvider}
}

// classifyErrForPersistence returns (error_kind, error_message) for the ai_jobs row.
func classifyErrForPersistence(err error) (kind, msg string) {
	if err == nil {
		return "", ""
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled", "context canceled"
	case errors.Is(err, myErrors.ErrContentPolicy):
		return "content_policy", err.Error()
	case errors.Is(err, myErrors.ErrAIProvider):
		return providerKind(err), err.Error()
	}
	return "internal", err.Error()
}

// providerKind returns a narrower provider error kind based on AppError.Code.
func providerKind(err error) string {
	var ae *myErrors.AppError
	if errors.As(err, &ae) && ae.Code != "" {
		return ae.Code
	}
	return "provider_5xx"
}

// statusFor maps a terminal error to an ai_jobs.status value.
func statusFor(err error) string {
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	return "failed"
}

// isWellFormedObject returns true when b parses as a JSON object.
// Used to drop garbled items without aborting the stream.
func isWellFormedObject(b []byte) bool {
	s := strings.TrimSpace(string(b))
	if !strings.HasPrefix(s, "{") {
		return false
	}
	var m map[string]json.RawMessage
	return json.Unmarshal(b, &m) == nil
}
