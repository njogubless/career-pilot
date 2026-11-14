package apptest

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/careerpilot/careerpilot/services/api/internal/discovery"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
	"github.com/careerpilot/careerpilot/services/api/internal/sources"
)

// fakeAdapter returns whatever snapshot the test configures.
type fakeAdapter struct {
	kind sources.Kind
	mu   sync.Mutex
	data map[string][]sources.Posting
	err  error
}

func newFake(kind sources.Kind) *fakeAdapter {
	return &fakeAdapter{kind: kind, data: map[string][]sources.Posting{}}
}

func (f *fakeAdapter) Kind() sources.Kind { return f.kind }

func (f *fakeAdapter) Fetch(_ context.Context, id string) ([]sources.Posting, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return append([]sources.Posting(nil), f.data[id]...), nil
}

func (f *fakeAdapter) set(id string, ps ...sources.Posting) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[id] = ps
	f.err = nil
}

func (f *fakeAdapter) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func posting(id, title, company, loc string) sources.Posting {
	status, scope, rc, region := sources.ClassifyLocation(loc, "")
	return sources.Posting{
		ExternalID: id, Title: title, Company: company, LocationRaw: loc, LocationCountries: sources.CountriesIn(loc),
		RemoteStatus: status, RemoteScope: scope, RemoteCountries: rc, RemoteRegion: region, EmploymentType: "FULL_TIME",
		CanonicalURL: "https://boards.example/" + id, ApplyURL: "https://boards.example/" + id + "/apply",
		DescriptionHTML: "<p>" + title + " role building Go services</p>", DescriptionText: title + " role building Go services",
	}
}

func addSource(t *testing.T, app *App, kind sources.Kind, ident string, minInterval int) uuid.UUID {
	t.Helper()
	id := ids.New()
	app.Exec(t, `INSERT INTO job_sources (id, kind, identifier, display_name, min_interval_minutes) VALUES ($1,$2,$3,$4,$5)`,
		id, kind, ident, ident, minInterval)
	return id
}

// allowRerun clears last_success_at so the minimum-interval guard does not skip.
func allowRerun(t *testing.T, app *App, id uuid.UUID) {
	app.Exec(t, `UPDATE job_sources SET last_success_at = now() - interval '1 day' WHERE id = $1`, id)
}

func TestDiscoverySnapshotLifecycle(t *testing.T) {
	gh := newFake(sources.Greenhouse)
	app := New(t, Options{Adapters: sources.Registry{sources.Greenhouse: gh}})
	src := addSource(t, app, sources.Greenhouse, "acme", 15)
	ctx := context.Background()

	a, b := posting("1", "Backend Engineer", "Acme", "Remote - US"), posting("2", "Data Engineer", "Acme", "Berlin, Germany")
	gh.set("acme", a, b, sources.Posting{ExternalID: "bad"}) // invalid posting is counted, not stored
	run, err := app.Discovery.RunSource(ctx, src, "MANUAL")
	if err != nil || run.Status != "SUCCEEDED" || run.Created != 2 || run.Invalid != 1 {
		t.Fatalf("first run: %+v err=%v", run, err)
	}

	// Minimum interval guard.
	run, _ = app.Discovery.RunSource(ctx, src, "MANUAL")
	if run.Status != "SKIPPED" {
		t.Fatalf("expected SKIPPED within min interval, got %+v", run)
	}

	allowRerun(t, app, src)
	b.Title = "Senior Data Engineer"
	gh.set("acme", a, b)
	run, _ = app.Discovery.RunSource(ctx, src, "SCHEDULED")
	if run.Unchanged != 1 || run.Updated != 1 || run.Expired != 0 {
		t.Fatalf("second run: %+v", run)
	}

	allowRerun(t, app, src)
	gh.set("acme", a) // b disappears from the board
	run, _ = app.Discovery.RunSource(ctx, src, "SCHEDULED")
	if run.Expired != 1 {
		t.Fatalf("expected 1 expired: %+v", run)
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM jobs WHERE expired_at IS NULL`); n != 1 {
		t.Fatalf("active jobs = %d", n)
	}

	allowRerun(t, app, src)
	b2 := posting("3", "Senior Data Engineer", "Acme", "Berlin, Germany") // reposted under a new id
	gh.set("acme", a, b2)
	run, _ = app.Discovery.RunSource(ctx, src, "SCHEDULED")
	if run.Reposted != 1 || run.Created != 1 {
		t.Fatalf("repost not detected: %+v", run)
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM jobs WHERE external_id = '3' AND repost_of IS NOT NULL`); n != 1 {
		t.Fatal("repost_of not recorded")
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM job_sources WHERE id = $1 AND consecutive_failures = 0 AND last_success_at IS NOT NULL`, src); n != 1 {
		t.Fatal("source health not recorded")
	}
}

func TestDiscoveryFailureIsolationAndBackoff(t *testing.T) {
	gh, lv := newFake(sources.Greenhouse), newFake(sources.Lever)
	app := New(t, Options{Adapters: sources.Registry{sources.Greenhouse: gh, sources.Lever: lv}})
	ghSrc := addSource(t, app, sources.Greenhouse, "acme", 15)
	lvSrc := addSource(t, app, sources.Lever, "globex", 15)
	ctx := context.Background()

	var ps []sources.Posting
	for i := 0; i < 12; i++ {
		ps = append(ps, posting(string(rune('a'+i)), "Role "+string(rune('A'+i)), "Acme", "Remote - US"))
	}
	gh.set("acme", ps...)
	lv.set("globex", posting("x", "Designer", "Globex", "London, United Kingdom"))
	if _, err := app.Discovery.RunSource(ctx, ghSrc, "MANUAL"); err != nil {
		t.Fatal(err)
	}

	// A failing source does not affect others and does not expire its jobs.
	allowRerun(t, app, ghSrc)
	gh.fail(&sources.SourceError{Status: 503, Msg: "unavailable"})
	run, err := app.Discovery.RunSource(ctx, ghSrc, "SCHEDULED")
	if err == nil || run.Status != "FAILED" {
		t.Fatalf("expected failure: %+v", run)
	}
	if _, err := app.Discovery.RunSource(ctx, lvSrc, "SCHEDULED"); err != nil {
		t.Fatalf("other source affected: %v", err)
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM jobs WHERE source_id = $1 AND expired_at IS NULL`, ghSrc); n != 12 {
		t.Fatalf("failed run expired jobs: active=%d", n)
	}

	// An empty snapshot for a board with many jobs is treated as suspicious.
	gh.set("acme")
	run, err = app.Discovery.RunSource(ctx, ghSrc, "SCHEDULED")
	if !errors.Is(err, discovery.ErrSuspicious) || run.Status != "FAILED" {
		t.Fatalf("empty snapshot should fail safely: %+v %v", run, err)
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM jobs WHERE source_id = $1 AND expired_at IS NULL`, ghSrc); n != 12 {
		t.Fatal("suspicious empty snapshot expired jobs")
	}

	// Circuit breaker: after repeated failures the source backs off.
	app.Exec(t, `UPDATE job_sources SET consecutive_failures = 5, last_run_at = now() WHERE id = $1`, ghSrc)
	run, _ = app.Discovery.RunSource(ctx, ghSrc, "SCHEDULED")
	if run.Status != "SKIPPED" {
		t.Fatalf("expected backoff skip: %+v", run)
	}
	var health string
	srcs, _ := discovery.ListSources(ctx, app.Pool, false)
	for _, s := range srcs {
		if s.ID == ghSrc {
			health = s.Health()
		}
	}
	if health != "DEGRADED" {
		t.Fatalf("health = %s", health)
	}
}

func TestCrossSourceDuplicatesCollapsedSameSourceKept(t *testing.T) {
	gh, ro := newFake(sources.Greenhouse), newFake(sources.RemoteOK)
	app := New(t, Options{Adapters: sources.Registry{sources.Greenhouse: gh, sources.RemoteOK: ro}})
	roSrc := addSource(t, app, sources.RemoteOK, "remoteok", 15)
	ghSrc := addSource(t, app, sources.Greenhouse, "acme", 15)
	ctx := context.Background()

	ro.set("remoteok", posting("r1", "Backend Engineer", "Acme", "Remote - US"))
	if _, err := app.Discovery.RunSource(ctx, roSrc, "MANUAL"); err != nil {
		t.Fatal(err)
	}
	// The employer ATS copy arrives later but wins as canonical; two identical
	// requisitions on the same board are both kept.
	gh.set("acme", posting("g1", "Backend Engineer", "Acme", "Remote - US"), posting("g2", "Backend Engineer", "Acme", "Remote - US"))
	if _, err := app.Discovery.RunSource(ctx, ghSrc, "MANUAL"); err != nil {
		t.Fatal(err)
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM jobs WHERE duplicate_of IS NULL AND expired_at IS NULL`); n != 2 {
		t.Fatalf("visible jobs = %d, want the two ATS requisitions", n)
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM jobs j WHERE j.external_id = 'r1' AND j.duplicate_of IS NOT NULL`); n != 1 {
		t.Fatal("aggregator copy not marked duplicate")
	}

	// When the ATS copies expire, the aggregator copy becomes visible again.
	allowRerun(t, app, ghSrc)
	gh.set("acme")
	if _, err := app.Discovery.RunSource(ctx, ghSrc, "MANUAL"); err != nil {
		t.Fatal(err)
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM jobs WHERE external_id = 'r1' AND duplicate_of IS NULL`); n != 1 {
		t.Fatal("aggregator copy not restored after canonical expired")
	}
}

func TestJobSearchAPI(t *testing.T) {
	gh := newFake(sources.Greenhouse)
	app := New(t, Options{Adapters: sources.Registry{sources.Greenhouse: gh}})
	src := addSource(t, app, sources.Greenhouse, "acme", 15)
	pay := posting("3", "Platform Engineer", "Acme", "Remote, Worldwide")
	lo, hi := 4000.0, 6000.0
	pay.Salary = &sources.Salary{Min: &lo, Max: &hi, Currency: "USD", Period: "MONTH"}
	gh.set("acme",
		posting("1", "Backend Engineer", "Acme", "Remote - US"),
		posting("2", "Product Designer", "Acme", "Nairobi, Kenya"),
		pay,
	)
	if _, err := app.Discovery.RunSource(context.Background(), src, "MANUAL"); err != nil {
		t.Fatal(err)
	}
	c := app.Client(t)
	c.Register("search@example.com")

	type page struct {
		Data []struct {
			ID     string
			Title  string
			Salary *struct{ Currency string }
			Source struct{ Kind string }
		}
		NextCursor *string `json:"next_cursor"`
		Total      int
	}
	get := func(q string) page {
		r := c.Do(http.MethodGet, "/api/v1/jobs?"+q, nil)
		if r.Code != http.StatusOK {
			t.Fatalf("GET /jobs?%s: %d %s", q, r.Code, r.Body)
		}
		var p page
		r.JSON(t, &p)
		return p
	}
	if p := get(""); p.Total != 3 {
		t.Fatalf("total = %d", p.Total)
	}
	if p := get("q=" + url.QueryEscape("backend")); p.Total != 1 || p.Data[0].Title != "Backend Engineer" {
		t.Fatalf("full-text: %+v", p)
	}
	// Country filter: located in KE, or remote-eligible for KE (worldwide).
	if p := get("country=KE"); p.Total != 2 {
		t.Fatalf("country=KE: %+v", p)
	}
	// Unknown salaries are kept unless disclosed-only is requested; currencies are not converted.
	if p := get("min_salary=60000&currency=USD"); p.Total != 3 {
		t.Fatalf("unknown salaries should be kept: %d", p.Total)
	}
	if p := get("min_salary=60000&currency=USD&salary_disclosed=true"); p.Total != 1 {
		t.Fatalf("disclosed: %d", p.Total)
	}
	if p := get("min_salary=60000&currency=EUR&salary_disclosed=true"); p.Total != 0 {
		t.Fatalf("different currency must not match: %d", p.Total)
	}
	// Pagination.
	p1 := get("limit=2")
	if len(p1.Data) != 2 || p1.NextCursor == nil {
		t.Fatalf("page1: %+v", p1)
	}
	p2 := get("limit=2&cursor=" + *p1.NextCursor)
	if len(p2.Data) != 1 || p2.NextCursor != nil {
		t.Fatalf("page2: %+v", p2)
	}
	// Validation.
	for _, q := range []string{"remote=SPACE", "country=Kenya", "posted_within=999", "min_salary=10", "cursor=garbage", "limit=500", "sort=random"} {
		if r := c.Do(http.MethodGet, "/api/v1/jobs?"+q, nil); r.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: want 422, got %d", q, r.Code)
		}
	}
	// Detail.
	var d struct {
		DescriptionHTML string `json:"description_html"`
		ApplyURL        string `json:"apply_url"`
	}
	c.Do(http.MethodGet, "/api/v1/jobs/"+p1.Data[0].ID, nil).JSON(t, &d)
	if d.ApplyURL == "" || d.DescriptionHTML == "" {
		t.Fatalf("detail: %+v", d)
	}
	if r := c.Do(http.MethodGet, "/api/v1/jobs/"+ids.New().String(), nil); r.Code != http.StatusNotFound {
		t.Fatalf("missing job: %d", r.Code)
	}
	var o struct {
		JobsActive int `json:"jobs_active"`
	}
	c.Do(http.MethodGet, "/api/v1/dashboard/overview", nil).JSON(t, &o)
	if o.JobsActive != 3 {
		t.Fatalf("overview: %+v", o)
	}
}

func TestSavedSearchesIsolationAndTriggerDedupe(t *testing.T) {
	app := New(t, Options{})
	alice := app.Client(t)
	alice.Register("saved-a@example.com")
	bob := app.Client(t)
	bob.Register("saved-b@example.com")

	r := alice.Do(http.MethodPost, "/api/v1/saved-searches", map[string]string{"name": "Remote Go", "query": "q=go&remote=REMOTE"})
	if r.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", r.Code, r.Body)
	}
	var s struct{ ID string }
	r.JSON(t, &s)
	if r := alice.Do(http.MethodPost, "/api/v1/saved-searches", map[string]string{"name": "Bad", "query": "remote=MARS"}); r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid filters saved: %d", r.Code)
	}
	var list struct{ Data []any }
	bob.Do(http.MethodGet, "/api/v1/saved-searches", nil).JSON(t, &list)
	if len(list.Data) != 0 {
		t.Fatal("bob sees alice's saved search")
	}
	if r := bob.Do(http.MethodDelete, "/api/v1/saved-searches/"+s.ID, nil); r.Code != http.StatusNotFound {
		t.Fatalf("cross-user delete: %d", r.Code)
	}

	var first, second struct{ Queued bool }
	alice.Do(http.MethodPost, "/api/v1/discovery/runs", nil).JSON(t, &first)
	bob.Do(http.MethodPost, "/api/v1/discovery/runs", nil).JSON(t, &second)
	if !first.Queued || second.Queued {
		t.Fatalf("manual triggers should be deduplicated: %+v %+v", first, second)
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM river_job WHERE kind = 'discover_all'`); n != 1 {
		t.Fatalf("discover_all jobs = %d", n)
	}
}

func TestAdminSourcesHiddenFromUsers(t *testing.T) {
	gh := newFake(sources.Greenhouse)
	gh.set("newco", posting("1", "Engineer", "NewCo", "Remote - US"))
	app := New(t, Options{Adapters: sources.Registry{sources.Greenhouse: gh}})
	c := app.Client(t)
	c.Register("admin@example.com")
	body := map[string]any{"kind": "GREENHOUSE", "identifier": "newco", "display_name": "NewCo"}
	if r := c.Do(http.MethodPost, "/api/v1/admin/job-sources", body); r.Code != http.StatusNotFound {
		t.Fatalf("non-admin reached admin route: %d", r.Code)
	}
	app.Exec(t, `UPDATE users SET role = 'admin' WHERE email = 'admin@example.com'`)
	if r := c.Do(http.MethodPost, "/api/v1/admin/job-sources", body); r.Code != http.StatusCreated {
		t.Fatalf("admin create: %d %s", r.Code, r.Body)
	}
	gh.fail(&sources.SourceError{Status: 404, Permanent: true, Msg: "board not found"})
	if r := c.Do(http.MethodPost, "/api/v1/admin/job-sources", map[string]any{"kind": "GREENHOUSE", "identifier": "ghost", "display_name": "Ghost"}); r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unfetchable board accepted: %d", r.Code)
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM audit_events WHERE action = 'admin.job_source.create'`); n != 1 {
		t.Fatal("admin action not audited")
	}
	_ = time.Now
}
