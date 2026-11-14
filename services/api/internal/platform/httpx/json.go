package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// WriteJSON writes v as JSON with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// DecodeJSON strictly decodes a single JSON object from the request body into
// dst. Unknown fields and trailing data are rejected. Body size is bounded by
// the MaxBody middleware.
func DecodeJSON(r *http.Request, dst any) error {
	if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "application/json") {
		return NewError(CodeValidationFailed, "Content-Type must be application/json.")
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		switch {
		case errors.As(err, &maxErr):
			return NewError(CodePayloadTooLarge, "The request body is too large.")
		case errors.Is(err, io.EOF):
			return NewError(CodeValidationFailed, "The request body is empty.")
		default:
			return &Error{Code: CodeValidationFailed, Message: "The request body is not valid JSON for this endpoint.", Cause: err}
		}
	}
	if dec.More() {
		return NewError(CodeValidationFailed, "The request body must contain a single JSON object.")
	}
	return nil
}
