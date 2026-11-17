package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/careerpilot/careerpilot/services/api/internal/aiclient"
	"github.com/careerpilot/careerpilot/services/api/internal/health"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/config"
)

func testDeps(ai *aiclient.Client) Deps {
	return Deps{
		Config:   config.Config{Env: config.EnvTest, Version: "test", MaxBodyBytes: 1 << 20},
		Logger:   slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Checkers: []health.Checker{{Name: "db", Required: true, Check: func(context.Context) error { return nil }}},
		AI:       ai,
	}
}

func getMeta(t *testing.T, h http.Handler) metaResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/meta", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var m metaResponse
	if err := json.NewDecoder(rec.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMetaWithoutAIServiceIsHonest(t *testing.T) {
	m := getMeta(t, NewRouter(testDeps(nil)))
	if m.Integrations["ai_service"].Status != StatusNotConfigured {
		t.Fatalf("ai_service = %+v", m.Integrations["ai_service"])
	}
	// Gmail is implemented; without OAuth credentials it is reported as not configured.
	if m.Integrations["gmail"].Status != StatusNotConfigured {
		t.Fatalf("gmail = %+v", m.Integrations["gmail"])
	}
	d := testDeps(nil)
	d.GmailReady = true
	if g := getMeta(t, NewRouter(d)).Integrations["gmail"]; g.Status != StatusAvailable {
		t.Fatalf("configured gmail = %+v", g)
	}
}

func TestMetaReflectsAIServiceInfo(t *testing.T) {
	var gotAuth string
	ai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(aiclient.Info{Service: "ai", LLMProvider: "none", LLMConfigured: false})
	}))
	defer ai.Close()

	m := getMeta(t, NewRouter(testDeps(aiclient.New(ai.URL, "tok", time.Second))))
	if gotAuth != "Bearer tok" {
		t.Fatalf("service token not sent: %q", gotAuth)
	}
	if m.Integrations["ai_service"].Status != StatusAvailable || m.Integrations["llm"].Status != StatusNotConfigured {
		t.Fatalf("integrations = %+v", m.Integrations)
	}
}

func TestMetaAIServiceDown(t *testing.T) {
	ai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ai.Close()
	m := getMeta(t, NewRouter(testDeps(aiclient.New(ai.URL, "tok", time.Second))))
	if m.Integrations["ai_service"].Status != StatusUnavailable {
		t.Fatalf("ai_service = %+v", m.Integrations["ai_service"])
	}
}

func TestUnknownRouteUsesEnvelopeAndSecurityHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	NewRouter(testDeps(nil)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rec.Code)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("X-Request-ID") == "" {
		t.Fatalf("missing headers: %v", rec.Header())
	}
	var body map[string]map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil || body["error"]["code"] != "NOT_FOUND" {
		t.Fatalf("body=%v err=%v", body, err)
	}
}

func TestHealthEndpoints(t *testing.T) {
	h := NewRouter(testDeps(nil))
	for _, p := range []string{"/healthz", "/readyz"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d", p, rec.Code)
		}
	}
}
