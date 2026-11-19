package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func runReady(t *testing.T, h *Handler) (int, readiness) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.Ready(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	var body readiness
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return rec.Code, body
}

func ok(context.Context) error { return nil }

func TestReadyAllOK(t *testing.T) {
	code, body := runReady(t, NewHandler("v", time.Second,
		Checker{Name: "db", Required: true, Check: ok},
		Checker{Name: "redis", Required: false, Check: func(context.Context) error { return ErrNotConfigured }},
	))
	if code != http.StatusOK || body.Status != "ready" {
		t.Fatalf("code=%d body=%+v", code, body)
	}
	if body.Checks["redis"].Status != "not_configured" {
		t.Fatalf("redis = %+v", body.Checks["redis"])
	}
}

func TestReadyRequiredFailure(t *testing.T) {
	code, body := runReady(t, NewHandler("v", time.Second,
		Checker{Name: "db", Required: true, Check: func(context.Context) error { return errors.New("down") }},
	))
	if code != http.StatusServiceUnavailable || body.Checks["db"].Status != "fail" {
		t.Fatalf("code=%d body=%+v", code, body)
	}
}

func TestReadyOptionalFailureDoesNotGate(t *testing.T) {
	code, _ := runReady(t, NewHandler("v", time.Second,
		Checker{Name: "db", Required: true, Check: ok},
		Checker{Name: "ai", Required: false, Check: func(context.Context) error { return errors.New("down") }},
	))
	if code != http.StatusOK {
		t.Fatalf("code=%d", code)
	}
}

func TestReadyTimeoutAndPanic(t *testing.T) {
	code, body := runReady(t, NewHandler("v", 50*time.Millisecond,
		Checker{Name: "slow", Required: true, Check: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}},
		Checker{Name: "panicky", Required: true, Check: func(context.Context) error { panic("x") }},
	))
	if code != http.StatusServiceUnavailable {
		t.Fatalf("code=%d", code)
	}
	if body.Checks["slow"].Status != "fail" || body.Checks["panicky"].Status != "fail" {
		t.Fatalf("checks=%+v", body.Checks)
	}
}

func TestReadinessFailsWhileDraining(t *testing.T) {
	var drain atomic.Bool
	h := NewHandler("v", time.Second, Checker{Name: "db", Required: true, Check: func(context.Context) error { return nil }}).WithDrain(&drain)
	if code, _ := runReady(t, h); code != http.StatusOK {
		t.Fatalf("before drain: %d", code)
	}
	drain.Store(true)
	code, body := runReady(t, h)
	if code != http.StatusServiceUnavailable || body.Status != "draining" {
		t.Fatalf("while draining: %d %+v", code, body)
	}
}
