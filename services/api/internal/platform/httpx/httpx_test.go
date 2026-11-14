package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func decodeEnvelope(t *testing.T, body io.Reader) errorPayload {
	t.Helper()
	var b errorBody
	if err := json.NewDecoder(body).Decode(&b); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return b.Error
}

func TestWriteErrorKnownCode(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	WriteError(rec, req, Validation(map[string]string{"email": "required"}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", rec.Code)
	}
	p := decodeEnvelope(t, rec.Body)
	if p.Code != CodeValidationFailed || p.Details["email"] != "required" {
		t.Fatalf("payload = %+v", p)
	}
}

func TestWriteErrorHidesInternalCause(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	WriteError(rec, req, errors.New("pq: password authentication failed for user secret"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatal("internal error detail leaked to client")
	}
}

func TestDecodeJSONStrict(t *testing.T) {
	type in struct {
		Name string `json:"name"`
	}
	cases := map[string]struct {
		body string
		ok   bool
	}{
		"valid":         {`{"name":"a"}`, true},
		"unknown field": {`{"name":"a","admin":true}`, false},
		"trailing":      {`{"name":"a"}{"name":"b"}`, false},
		"empty":         {``, false},
		"malformed":     {`{"name":`, false},
	}
	t.Run("missing content type", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"a"}`))
		var dst in
		if err := DecodeJSON(r, &dst); err == nil {
			t.Fatal("requests without application/json must be rejected (CSRF defence)")
		}
	})
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			var dst in
			err := DecodeJSON(r, &dst)
			if (err == nil) != tc.ok {
				t.Fatalf("err = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

func TestDecodeJSONTooLarge(t *testing.T) {
	h := MaxBody(8)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v map[string]any
		if err := DecodeJSON(r, &v); err != nil {
			WriteError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"a":"0123456789"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestRequestIDAndRecover(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := RequestID(logger)(Recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "bad id with spaces")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	id := rec.Header().Get("X-Request-ID")
	if id == "" || strings.Contains(id, " ") {
		t.Fatalf("request id not regenerated: %q", id)
	}
	if p := decodeEnvelope(t, rec.Body); p.RequestID != id {
		t.Fatalf("envelope request_id %q != header %q", p.RequestID, id)
	}
}
