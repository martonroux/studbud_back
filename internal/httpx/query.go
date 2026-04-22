package httpx

import (
	"net/http"
	"strconv"

	"studbud/backend/internal/myErrors"
)

// QueryInt64 reads a required int64 query param by name.
// Returns ErrInvalidInput if the param is missing or not parseable.
func QueryInt64(r *http.Request, name string) (int64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, myErrors.ErrInvalidInput
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, myErrors.ErrInvalidInput
	}
	return v, nil
}
