// Package httpx contains HTTP helpers shared by all modules: the error
// envelope, JSON encoding/decoding and common middleware.
package httpx

import (
	"errors"
	"net/http"

	"github.com/careerpilot/careerpilot/services/api/internal/platform/logging"
)

// Code is a stable, machine-readable error code (see docs/API_CONTRACTS.md).
type Code string

const (
	CodeUnauthenticated   Code = "UNAUTHENTICATED"
	CodeForbidden         Code = "FORBIDDEN"
	CodeNotFound          Code = "NOT_FOUND"
	CodeValidationFailed  Code = "VALIDATION_FAILED"
	CodeConflict          Code = "CONFLICT"
	CodeInvalidTransition Code = "INVALID_STATE_TRANSITION"
	CodeRateLimited       Code = "RATE_LIMITED"
	CodePayloadTooLarge   Code = "PAYLOAD_TOO_LARGE"
	CodeUnavailable       Code = "SERVICE_UNAVAILABLE"
	CodeInternal          Code = "INTERNAL"
)

var codeStatus = map[Code]int{
	CodeUnauthenticated:   http.StatusUnauthorized,
	CodeForbidden:         http.StatusForbidden,
	CodeNotFound:          http.StatusNotFound,
	CodeValidationFailed:  http.StatusUnprocessableEntity,
	CodeConflict:          http.StatusConflict,
	CodeInvalidTransition: http.StatusConflict,
	CodeRateLimited:       http.StatusTooManyRequests,
	CodePayloadTooLarge:   http.StatusRequestEntityTooLarge,
	CodeUnavailable:       http.StatusServiceUnavailable,
	CodeInternal:          http.StatusInternalServerError,
}

// Error is an error that maps onto the public error envelope. Message must be
// safe to show to end users; internal causes go in Cause and are only logged.
type Error struct {
	Code    Code
	Message string
	Details map[string]string
	Cause   error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return string(e.Code) + ": " + e.Message + ": " + e.Cause.Error()
	}
	return string(e.Code) + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.Cause }

func NewError(code Code, msg string) *Error { return &Error{Code: code, Message: msg} }

func Validation(details map[string]string) *Error {
	return &Error{Code: CodeValidationFailed, Message: "The request is invalid.", Details: details}
}

type errorBody struct {
	Error errorPayload `json:"error"`
}

type errorPayload struct {
	Code      Code              `json:"code"`
	Message   string            `json:"message"`
	Details   map[string]string `json:"details,omitempty"`
	RequestID string            `json:"request_id,omitempty"`
}

// WriteError renders err as the standard envelope. Unknown errors become
// INTERNAL with a generic message; their detail is logged, never returned.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var e *Error
	if !errors.As(err, &e) {
		e = &Error{Code: CodeInternal, Message: "An unexpected error occurred.", Cause: err}
	}
	status, ok := codeStatus[e.Code]
	if !ok {
		status = http.StatusInternalServerError
	}
	if status >= 500 {
		logging.FromContext(r.Context()).Error("request failed", "code", e.Code, "err", err)
	}
	WriteJSON(w, status, errorBody{Error: errorPayload{
		Code:      e.Code,
		Message:   e.Message,
		Details:   e.Details,
		RequestID: RequestIDFrom(r.Context()),
	}})
}
