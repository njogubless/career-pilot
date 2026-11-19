package apptest

import (
	"net/http"
	"regexp"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/careerpilot/careerpilot/services/api/internal/platform/observability"
)

var uuidLike = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

func TestGlobalRateLimitIsPerUser(t *testing.T) {
	app := New(t, Options{RateLimitPerMinute: 15})
	a, b := app.Client(t), app.Client(t)
	a.Register("limit-a@example.com")
	b.Register("limit-b@example.com")

	var limited Response
	for i := 0; i < 20; i++ {
		if r := a.Do(http.MethodGet, "/api/v1/notifications", nil); r.Code == http.StatusTooManyRequests {
			limited = r
			break
		} else if r.Code != http.StatusOK {
			t.Fatalf("request %d: %d %s", i, r.Code, r.Body)
		}
	}
	if limited.Code != http.StatusTooManyRequests || limited.ErrCode() != "RATE_LIMITED" || limited.Header.Get("Retry-After") == "" {
		t.Fatalf("expected 429 RATE_LIMITED with Retry-After, got %d %s %v", limited.Code, limited.Body, limited.Header)
	}
	// Another user on the same address is unaffected: the key is the user.
	if r := b.Do(http.MethodGet, "/api/v1/notifications", nil); r.Code != http.StatusOK {
		t.Fatalf("other user limited: %d", r.Code)
	}
	// Health probes are never limited.
	for i := 0; i < 20; i++ {
		if r := a.Do(http.MethodGet, "/healthz", nil); r.Code != http.StatusOK {
			t.Fatalf("healthz limited: %d", r.Code)
		}
	}
}

func TestMetricsUseRoutePatternsAndStayInternal(t *testing.T) {
	app := New(t, Options{})
	c := app.Client(t)
	c.Register("metrics@example.com")
	id := uuid.New().String()
	if r := c.Do(http.MethodPost, "/api/v1/notifications/"+id+"/read", nil); r.Code != http.StatusNotFound {
		t.Fatalf("read unknown notification: %d", r.Code)
	}
	// /metrics is never served by the public router.
	if r := c.Do(http.MethodGet, "/metrics", nil); r.Code != http.StatusNotFound {
		t.Fatalf("public /metrics: %d", r.Code)
	}

	families, err := app.Metrics.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	routes := map[string]bool{}
	for _, f := range families {
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if uuidLike.MatchString(l.GetValue()) {
					t.Fatalf("metric %s has an id in label %s=%s", f.GetName(), l.GetName(), l.GetValue())
				}
				if f.GetName() == "careerpilot_http_requests_total" && l.GetName() == "route" {
					routes[l.GetValue()] = true
				}
			}
		}
	}
	if !routes["/api/v1/notifications/{id}/read"] || !routes["/api/v1/auth/register"] {
		t.Fatalf("expected route patterns, got %v", routes)
	}
	if !routes["unmatched"] {
		t.Fatalf("unknown routes should be grouped as unmatched, got %v", routes)
	}
}

func TestQueueCollectorReadsPersistedState(t *testing.T) {
	app := New(t, Options{})
	reg := prometheus.NewRegistry()
	reg.MustRegister(observability.NewQueueCollector(app.Pool), observability.NewPoolCollector(app.Pool))
	app.Exec(t, `INSERT INTO river_job (kind, args, state, max_attempts, scheduled_at, queue)
		VALUES ('probe_job', '{}', 'available', 3, now() - interval '90 seconds', 'default')`)
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, f := range families {
		for _, m := range f.GetMetric() {
			v := m.GetGauge().GetValue() + m.GetCounter().GetValue()
			key := f.GetName()
			for _, l := range m.GetLabel() {
				key += "," + l.GetValue()
			}
			got[key] = v
		}
	}
	if got["careerpilot_queue_collector_up"] != 1 {
		t.Fatalf("collector failed: %v", got)
	}
	if got["careerpilot_queue_jobs,probe_job,available"] != 1 {
		t.Fatalf("queued job not counted: %v", got)
	}
	if got["careerpilot_queue_oldest_available_seconds"] < 60 {
		t.Fatalf("oldest available age: %v", got["careerpilot_queue_oldest_available_seconds"])
	}
	if _, ok := got["careerpilot_schedules_overdue"]; !ok {
		t.Fatalf("overdue schedules gauge missing")
	}
	if got["careerpilot_db_pool_max_connections"] < 1 {
		t.Fatalf("pool stats missing: %v", got)
	}
}
