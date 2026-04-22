package httpx

import (
	"encoding/json"
	"errors"
	"net/http"

	"studbud/backend/internal/myErrors"
)

type errorBody struct {
	Error errorDetails `json:"error"`
}

type errorDetails struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

// WriteError writes a JSON error envelope with HTTP status mapped from the sentinel.
// If the error is an *myErrors.AppError with Status != 0, that overrides the mapping.
func WriteError(w http.ResponseWriter, err error) {
	status := mapStatus(err)
	code, message, field := describe(err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Error: errorDetails{Code: code, Message: message, Field: field}})
}

func mapStatus(err error) int {
	var ae *myErrors.AppError
	if errors.As(err, &ae) && ae.Status != 0 {
		return ae.Status
	}
	switch {
	case errors.Is(err, myErrors.ErrUnauthenticated):
		return http.StatusUnauthorized
	case errors.Is(err, myErrors.ErrNotVerified),
		errors.Is(err, myErrors.ErrForbidden),
		errors.Is(err, myErrors.ErrAdminRequired):
		return http.StatusForbidden
	case errors.Is(err, myErrors.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, myErrors.ErrConflict),
		errors.Is(err, myErrors.ErrAlreadyVerified):
		return http.StatusConflict
	case errors.Is(err, myErrors.ErrInvalidInput),
		errors.Is(err, myErrors.ErrValidation):
		return http.StatusBadRequest
	case errors.Is(err, myErrors.ErrNoAIAccess):
		return http.StatusPaymentRequired
	case errors.Is(err, myErrors.ErrQuotaExhausted),
		errors.Is(err, myErrors.ErrPdfTooLarge):
		return http.StatusTooManyRequests
	case errors.Is(err, myErrors.ErrAIProvider),
		errors.Is(err, myErrors.ErrStripe):
		return http.StatusBadGateway
	case errors.Is(err, myErrors.ErrNotImplemented):
		return http.StatusNotImplemented
	default:
		return http.StatusInternalServerError
	}
}

func describe(err error) (code, message, field string) {
	var ae *myErrors.AppError
	if errors.As(err, &ae) {
		return orDefault(ae.Code, sentinelCode(ae.Wrapped)), ae.Message, ae.Field
	}
	return sentinelCode(err), err.Error(), ""
}

func sentinelCode(err error) string {
	switch {
	case errors.Is(err, myErrors.ErrUnauthenticated):
		return "unauthenticated"
	case errors.Is(err, myErrors.ErrNotVerified):
		return "not_verified"
	case errors.Is(err, myErrors.ErrForbidden):
		return "forbidden"
	case errors.Is(err, myErrors.ErrAdminRequired):
		return "admin_required"
	case errors.Is(err, myErrors.ErrNotFound):
		return "not_found"
	case errors.Is(err, myErrors.ErrConflict):
		return "conflict"
	case errors.Is(err, myErrors.ErrAlreadyVerified):
		return "already_verified"
	case errors.Is(err, myErrors.ErrInvalidInput):
		return "invalid_input"
	case errors.Is(err, myErrors.ErrValidation):
		return "validation"
	case errors.Is(err, myErrors.ErrNoAIAccess):
		return "no_ai_access"
	case errors.Is(err, myErrors.ErrQuotaExhausted):
		return "quota_exhausted"
	case errors.Is(err, myErrors.ErrPdfTooLarge):
		return "pdf_too_large"
	case errors.Is(err, myErrors.ErrAIProvider):
		return "ai_provider_error"
	case errors.Is(err, myErrors.ErrStripe):
		return "stripe_error"
	case errors.Is(err, myErrors.ErrNotImplemented):
		return "not_implemented"
	default:
		return "internal_error"
	}
}

func orDefault(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}
