package sources

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Synthetic fixtures mirroring each provider's documented/observed shape
// (verified against live responses on 2026-11-14; no third-party content stored).
var fixtures = map[string]string{
	"/v1/boards/acme/jobs": `{"jobs":[{"id":101,"title":"Backend Engineer","absolute_url":"https://job-boards.greenhouse.io/acme/jobs/101",
		"company_name":"Acme","first_published":"2026-11-01T10:00:00-05:00","updated_at":"2026-11-02T10:00:00-05:00",
		"content":"&lt;p&gt;Build APIs&lt;/p&gt;&lt;script&gt;alert(1)&lt;/script&gt;","location":{"name":"Remote, United States"},
		"departments":[{"name":"Engineering"}],"offices":[{"name":"United States of America"}]}],"meta":{"total":1}}`,
	"/v0/postings/acme": `[{"id":"lev-1","text":"Data Engineer","hostedUrl":"https://jobs.lever.co/acme/lev-1","applyUrl":"https://jobs.lever.co/acme/lev-1/apply",
		"createdAt":1793000000000,"country":"GB","workplaceType":"hybrid","description":"<p>Pipelines</p>",
		"lists":[{"text":"Requirements","content":"<li>SQL</li>"}],"additional":"","categories":{"commitment":"Full-time","location":"London, United Kingdom","team":"Data"},
		"salaryRange":{"min":60000,"max":80000,"currency":"GBP","interval":"per-year-salary"}}]`,
	"/posting-api/job-board/acme": `{"jobs":[{"id":"ash-1","title":"Platform Engineer","department":"Eng","employmentType":"FullTime","location":"Remote (Canada)",
		"secondaryLocations":[],"publishedAt":"2026-11-03T00:00:00.000+00:00","isListed":true,"isRemote":true,"workplaceType":"Remote",
		"jobUrl":"https://jobs.ashbyhq.com/acme/ash-1","applyUrl":"https://jobs.ashbyhq.com/acme/ash-1/application","descriptionHtml":"<p>Infra</p>",
		"compensation":{"compensationTierSummary":"CA$150K – CA$180K","summaryComponents":[{"compensationType":"Salary","interval":"1 YEAR","currencyCode":"CAD","minValue":150000,"maxValue":180000}]}},
		{"id":"ash-hidden","title":"Unlisted","isListed":false,"jobUrl":"https://jobs.ashbyhq.com/acme/x","applyUrl":"https://jobs.ashbyhq.com/acme/x"}]}`,
	"/api": `[{"last_updated":1,"legal":"API Terms of Service: link back"},
		{"id":"ro-1","date":"2026-11-04T08:00:00+00:00","company":"Globex","position":"Frontend Dev","tags":["react"],"description":"<p>UI</p>",
		"location":"Worldwide","apply_url":"https://remoteok.com/remote-jobs/ro-1","url":"https://remoteok.com/remote-jobs/ro-1","salary_min":0,"salary_max":0}]`,
}

func testServer(t *testing.T) (*httptest.Server, *Fetcher) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := fixtures[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("User-Agent") != "test-agent" {
			http.Error(w, "missing UA", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	f := NewFetcher(FetcherOptions{UserAgent: "test-agent", Hosts: []string{u.Hostname()}, PerSecond: 1000, BaseBackoff: time.Millisecond, Transport: http.DefaultTransport})
	return srv, f
}

func TestGreenhouseAdapter(t *testing.T) {
	srv, f := testServer(t)
	ps, err := (&greenhouseAdapter{f: f, base: srv.URL}).Fetch(context.Background(), "acme")
	if err != nil || len(ps) != 1 {
		t.Fatalf("fetch: %v %d", err, len(ps))
	}
	p := ps[0]
	if p.ExternalID != "101" || p.Company != "Acme" || p.RemoteStatus != "REMOTE" || p.RemoteScope != ScopeCountries || p.RemoteCountries[0] != "US" {
		t.Fatalf("normalisation: %+v", p)
	}
	if strings.Contains(p.DescriptionHTML, "script") || !strings.Contains(p.DescriptionHTML, "<p>Build APIs</p>") {
		t.Fatalf("html not unescaped+sanitised: %q", p.DescriptionHTML)
	}
	if p.Salary != nil || p.EmploymentType != "UNKNOWN" {
		t.Fatal("greenhouse does not publish salary/employment type here; must stay unknown")
	}
}

func TestLeverAdapter(t *testing.T) {
	srv, f := testServer(t)
	ps, err := (&leverAdapter{f: f, base: srv.URL}).Fetch(context.Background(), "acme")
	if err != nil || len(ps) != 1 {
		t.Fatalf("fetch: %v", err)
	}
	p := ps[0]
	if p.RemoteStatus != "HYBRID" || p.EmploymentType != "FULL_TIME" || p.Salary == nil || p.Salary.Currency != "GBP" || p.Salary.Period != "YEAR" {
		t.Fatalf("normalisation: %+v %+v", p, p.Salary)
	}
	if !strings.Contains(p.DescriptionText, "Requirements") || p.PostedAt == nil {
		t.Fatalf("lists/date missing: %q", p.DescriptionText)
	}
}

func TestAshbyAdapterSkipsUnlisted(t *testing.T) {
	srv, f := testServer(t)
	ps, err := (&ashbyAdapter{f: f, base: srv.URL}).Fetch(context.Background(), "acme")
	if err != nil || len(ps) != 1 {
		t.Fatalf("fetch: %v %d", err, len(ps))
	}
	p := ps[0]
	if p.RemoteScope != ScopeCountries || p.RemoteCountries[0] != "CA" || p.Salary == nil || *p.Salary.Min != 150000 || p.Salary.Currency != "CAD" {
		t.Fatalf("normalisation: %+v", p)
	}
}

func TestRemoteOKAdapterSkipsLegalNotice(t *testing.T) {
	srv, f := testServer(t)
	ps, err := (&remoteOKAdapter{f: f, base: srv.URL}).Fetch(context.Background(), "")
	if err != nil || len(ps) != 1 {
		t.Fatalf("fetch: %v %d", err, len(ps))
	}
	if ps[0].RemoteScope != ScopeWorldwide || ps[0].Salary != nil {
		t.Fatalf("salary 0 must be undisclosed; scope worldwide: %+v", ps[0])
	}
}

func TestInvalidIdentifierRejectedBeforeRequest(t *testing.T) {
	srv, f := testServer(t)
	for _, id := range []string{"../admin", "a/b", "", "acme?x=1"} {
		_, err := (&greenhouseAdapter{f: f, base: srv.URL}).Fetch(context.Background(), id)
		var se *SourceError
		if !errors.As(err, &se) || !se.Permanent {
			t.Errorf("identifier %q: want permanent error, got %v", id, err)
		}
	}
}

func TestFetcherRetriesThenSucceeds(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	f := NewFetcher(FetcherOptions{Hosts: []string{u.Hostname()}, PerSecond: 1000, BaseBackoff: time.Millisecond, MaxAttempts: 3, Transport: http.DefaultTransport})
	var out map[string]bool
	if err := f.GetJSON(context.Background(), srv.URL, &out); err != nil || !out["ok"] || hits != 3 {
		t.Fatalf("err=%v hits=%d", err, hits)
	}
}

func TestFetcherPermanentAndGuards(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/missing":
			http.NotFound(w, r)
		case "/big":
			_, _ = w.Write([]byte(`"` + strings.Repeat("x", 2048) + `"`))
		case "/redirect":
			http.Redirect(w, r, "https://evil.example/steal", http.StatusFound)
		}
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	f := NewFetcher(FetcherOptions{Hosts: []string{u.Hostname()}, PerSecond: 1000, MaxBytes: 1024, BaseBackoff: time.Millisecond, Transport: http.DefaultTransport})
	var out any
	var se *SourceError
	if err := f.GetJSON(context.Background(), srv.URL+"/missing", &out); !errors.As(err, &se) || !se.Permanent || se.Status != 404 {
		t.Fatalf("404 should be permanent: %v", err)
	}
	if err := f.GetJSON(context.Background(), srv.URL+"/big", &out); !errors.As(err, &se) || !se.Permanent {
		t.Fatalf("oversized body should fail permanently: %v", err)
	}
	if err := f.GetJSON(context.Background(), "https://evil.example/x", &out); err == nil {
		t.Fatal("disallowed host fetched")
	}
	if err := f.GetJSON(context.Background(), srv.URL+"/redirect", &out); err == nil {
		t.Fatal("redirect to disallowed host followed")
	}
}
