package apptest

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
	"github.com/careerpilot/careerpilot/services/api/internal/sources"
)

// fakeAI implements the AI service's matching endpoints deterministically:
// a tiny catalogue, "Requirements:"/"Nice to have:" sections, and hashed
// bag-of-words embeddings.
type fakeAI struct {
	llm bool
}

var fakeCatalogue = map[string][2]string{ // lower-case term -> slug, name
	"go": {"go", "Go"}, "golang": {"go", "Go"}, "postgresql": {"postgresql", "PostgreSQL"}, "postgres": {"postgresql", "PostgreSQL"},
	"kafka": {"kafka", "Kafka"}, "rust": {"rust", "Rust"}, "react": {"react", "React"}, "typescript": {"typescript", "TypeScript"},
	"terraform": {"terraform", "Terraform"},
}

func (f *fakeAI) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /internal/v1/info", func(w http.ResponseWriter, r *http.Request) {
		caps := []string{"info", "jobs.analyze", "skills.normalize", "embeddings"}
		if f.llm {
			caps = append(caps, "jobs.analyze.llm")
		}
		writeJSON(w, map[string]any{"llm_configured": f.llm, "llm_provider": map[bool]string{true: "fake", false: "none"}[f.llm],
			"capabilities": caps, "embedding_model": "fake-emb", "catalogue_version": "tax-test",
			"analyzer_versions": map[string]string{"jobs.deterministic": "jd-test+tax-test"}})
	})
	mux.HandleFunc("POST /internal/v1/skills/normalize", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Terms, Texts []string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		terms := []map[string]any{}
		for _, t := range in.Terms {
			var skill any
			if c, ok := fakeCatalogue[strings.ToLower(strings.TrimSpace(t))]; ok {
				skill = map[string]string{"slug": c[0], "name": c[1], "category": "Tech"}
			}
			terms = append(terms, map[string]any{"term": t, "skill": skill})
		}
		texts := [][]map[string]string{}
		for _, t := range in.Texts {
			seen := map[string]bool{}
			ms := []map[string]string{}
			for _, word := range words(t) {
				if c, ok := fakeCatalogue[word]; ok && !seen[c[0]] {
					seen[c[0]] = true
					ms = append(ms, map[string]string{"slug": c[0], "name": c[1], "category": "Tech", "evidence": t})
				}
			}
			texts = append(texts, ms)
		}
		writeJSON(w, map[string]any{"catalogue_version": "tax-test", "terms": terms, "texts": texts})
	})
	mux.HandleFunc("POST /internal/v1/embeddings", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Texts []string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		vecs := [][]float64{}
		for _, t := range in.Texts {
			vecs = append(vecs, embed(t))
		}
		writeJSON(w, map[string]any{"model": "fake-emb", "dim": 384, "vectors": vecs})
	})
	mux.HandleFunc("POST /internal/v1/jobs/analyze", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Mode  string
			Embed bool
			Jobs  []struct{ ID, Title, Company, Text string }
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Mode == "llm" && !f.llm {
			w.WriteHeader(http.StatusServiceUnavailable)
			writeJSON(w, map[string]any{"error": map[string]string{"code": "AI_PROVIDER_UNAVAILABLE", "message": "none"}})
			return
		}
		results := []map[string]any{}
		for _, j := range in.Jobs {
			res := map[string]any{"id": j.ID, "analysis": fakeAnalysis(j.Title, j.Text, in.Mode)}
			if in.Embed {
				res["embedding"] = embed(j.Title + "\n" + j.Text)
			}
			results = append(results, res)
		}
		out := map[string]any{"results": results, "embedding_model": nil}
		if in.Embed {
			out["embedding_model"] = "fake-emb"
		}
		if in.Mode == "llm" {
			out["usage"] = map[string]any{"provider": "fake", "model": "fake-llm", "prompt_version": "jd_analyze@1", "input_tokens": 10, "output_tokens": 5}
		}
		writeJSON(w, out)
	})
	return mux
}

func fakeAnalysis(title, text, mode string) map[string]any {
	method, version := "DETERMINISTIC", "jd-test+tax-test"
	if mode == "llm" {
		method, version = "LLM", "jd_analyze@1+tax-test"
	}
	importance := "MENTIONED"
	seen := map[string]bool{}
	skills := []map[string]any{}
	for _, line := range strings.Split(text, "\n") {
		switch strings.TrimSpace(strings.ToLower(line)) {
		case "requirements:":
			importance = "REQUIRED"
			continue
		case "nice to have:":
			importance = "PREFERRED"
			continue
		}
		for _, word := range words(line) {
			if c, ok := fakeCatalogue[word]; ok && !seen[c[0]] {
				seen[c[0]] = true
				skills = append(skills, map[string]any{"slug": c[0], "name": c[1], "importance": importance, "evidence": strings.TrimSpace(line)})
			}
		}
	}
	seniority := "UNKNOWN"
	if strings.Contains(strings.ToLower(title), "senior") {
		seniority = "SENIOR"
	}
	return map[string]any{"method": method, "analyzer_version": version, "skills": skills, "min_years": nil,
		"seniority": seniority, "sponsorship": "UNKNOWN", "work_authorization": []string{}, "degree": "NONE_STATED", "warnings": []string{}}
}

func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') })
}

func embed(text string) []float64 {
	v := make([]float64, 384)
	for _, w := range words(text) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(w))
		v[h.Sum32()%384]++
	}
	var n float64
	for _, x := range v {
		n += x * x
	}
	n = math.Sqrt(n)
	for i := range v {
		if n > 0 {
			v[i] /= n
		}
	}
	return v
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func withText(p sources.Posting, text string) sources.Posting {
	p.DescriptionText, p.DescriptionHTML = text, "<p>"+text+"</p>"
	return p
}

type matchPage struct {
	Data []struct {
		Job struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"job"`
		Score         float64  `json:"score"`
		Eligibility   string   `json:"eligibility"`
		MissingSkills []string `json:"missing_skills"`
		Blockers      []string `json:"blockers"`
	} `json:"data"`
	Total int `json:"total"`
	State struct {
		Status string `json:"status"`
	} `json:"state"`
}

func userIDByEmail(t *testing.T, app *App, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := app.Pool.QueryRow(context.Background(), `SELECT id FROM users WHERE email = $1`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func setupMatching(t *testing.T, llm bool) (*App, map[string]string) {
	t.Helper()
	ai := httptest.NewServer((&fakeAI{llm: llm}).handler())
	t.Cleanup(ai.Close)
	gh := newFake(sources.Greenhouse)
	app := New(t, Options{AIURL: ai.URL, Adapters: sources.Registry{sources.Greenhouse: gh}})
	src := addSource(t, app, sources.Greenhouse, "acme", 15)
	gh.set("acme",
		withText(posting("a", "Senior Backend Engineer", "Acme", "Remote - Worldwide"), "Build payment services.\nRequirements:\nGo and PostgreSQL\nKafka\nNice to have:\nTerraform"),
		withText(posting("b", "Frontend Engineer", "Acme", "Berlin, Germany"), "Requirements:\nReact\nTypeScript"),
		withText(posting("c", "Backend Engineer", "Acme", "Remote - US"), "Requirements:\nGo\nRust"),
	)
	if run, err := app.Discovery.RunSource(context.Background(), src, "MANUAL"); err != nil || run.Created != 3 {
		t.Fatalf("discovery: %+v %v", run, err)
	}
	for {
		n, more, err := app.Matching.AnalyzePending(context.Background())
		if err != nil {
			t.Fatalf("analyze: %v", err)
		}
		if n == 0 || !more {
			break
		}
	}
	jobs := map[string]string{}
	rows, err := app.Pool.Query(context.Background(), `SELECT external_id, id::text FROM jobs`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var ext, id string
		_ = rows.Scan(&ext, &id)
		jobs[ext] = id
	}
	return app, jobs
}

func buildProfile(t *testing.T, c *Client) {
	t.Helper()
	for _, step := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPut, "/api/v1/profiles/me/basics", map[string]any{"headline": "Backend Engineer building payment services in Go",
			"summary": "", "location_city": "Nairobi", "location_country": "KE", "years_experience": 6, "links": []any{}}},
		{http.MethodPut, "/api/v1/profiles/me/preferences", map[string]any{"target_roles": []string{"Backend Engineer"}, "industries": []string{},
			"employment_types": []string{"FULL_TIME"}, "preferred_locations": []string{}, "remote_mode": "REMOTE_ONLY",
			"work_eligible_countries": []string{"KE"}, "seniority_levels": []string{"SENIOR"}}},
		{http.MethodPost, "/api/v1/profiles/me/skills", map[string]any{"name": "Golang"}},
		{http.MethodPost, "/api/v1/profiles/me/skills", map[string]any{"name": "PostgreSQL"}},
	} {
		if r := c.Do(step.method, step.path, step.body); r.Code >= 300 {
			t.Fatalf("%s %s: %d %s", step.method, step.path, r.Code, r.Body)
		}
	}
}

func TestMatchingEndToEnd(t *testing.T) {
	app, jobs := setupMatching(t, false)
	ctx := context.Background()
	if n := app.QueryInt(t, `SELECT count(*) FROM job_analyses WHERE method = 'DETERMINISTIC'`); n != 3 {
		t.Fatalf("analyses = %d", n)
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM job_embeddings WHERE model = 'fake-emb'`); n != 3 {
		t.Fatalf("embeddings = %d", n)
	}

	c := app.Client(t)
	c.Register("match@example.com")
	uid := userIDByEmail(t, app, "match@example.com")

	// Before a profile exists, matching is honest about why there is nothing.
	var early struct{ Status, Message string }
	c.Do(http.MethodGet, "/api/v1/jobs/"+jobs["a"]+"/match", nil).JSON(t, &early)
	if early.Status != "PROFILE_INCOMPLETE" {
		t.Fatalf("status %s", early.Status)
	}

	buildProfile(t, c)
	// Profile writes queue a debounced recomputation.
	if n := app.QueryInt(t, `SELECT count(*) FROM river_job WHERE kind = 'match_user' AND args->>'user_id' = $1`, uid.String()); n != 1 {
		t.Fatalf("match_user jobs queued = %d (expected 1, debounced)", n)
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM match_states WHERE user_id = $1 AND status = 'QUEUED'`, uid); n != 1 {
		t.Fatal("match state not QUEUED")
	}

	stats, err := app.Matching.ComputeForUser(ctx, uid)
	if err != nil || stats.Scored != 3 {
		t.Fatalf("compute: %+v %v", stats, err)
	}

	var page matchPage
	c.Do(http.MethodGet, "/api/v1/matches", nil).JSON(t, &page)
	// Default list: everything not ineligible. "Berlin, Germany" does not state
	// the arrangement, so for a remote-only user it is UNKNOWN, not rejected.
	if page.State.Status != "READY" || page.Total != 2 || page.Data[0].Job.ID != jobs["a"] || page.Data[0].Eligibility != "ELIGIBLE" ||
		page.Data[1].Job.ID != jobs["b"] || page.Data[1].Eligibility != "UNKNOWN" {
		t.Fatalf("default list: %+v", page)
	}
	if strings.Join(page.Data[0].MissingSkills, ",") != "Kafka" {
		t.Fatalf("missing skills: %v", page.Data[0].MissingSkills)
	}
	c.Do(http.MethodGet, "/api/v1/matches?eligibility=ALL", nil).JSON(t, &page)
	if page.Total != 3 {
		t.Fatalf("ALL total = %d", page.Total)
	}
	for _, it := range page.Data {
		if it.Job.ID == jobs["c"] && (it.Eligibility != "INELIGIBLE" || len(it.Blockers) == 0) {
			t.Fatalf("US-only remote job should be ineligible with a blocker: %+v", it)
		}
	}
	if r := c.Do(http.MethodGet, "/api/v1/matches?eligibility=MAYBE", nil); r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid filter: %d", r.Code)
	}

	// Reproducible: recomputing with unchanged inputs yields identical results.
	before := app.QueryInt(t, `SELECT count(DISTINCT md5(result::text || overall_score::text || profile_hash || job_hash)) FROM job_match_results WHERE user_id = $1`, uid)
	snapshot := map[string]string{}
	rows, _ := app.Pool.Query(ctx, `SELECT job_id::text, result::text || profile_hash || job_hash FROM job_match_results WHERE user_id = $1`, uid)
	for rows.Next() {
		var k, v string
		_ = rows.Scan(&k, &v)
		snapshot[k] = v
	}
	if _, err := app.Matching.ComputeForUser(ctx, uid); err != nil {
		t.Fatal(err)
	}
	rows, _ = app.Pool.Query(ctx, `SELECT job_id::text, result::text || profile_hash || job_hash FROM job_match_results WHERE user_id = $1`, uid)
	for rows.Next() {
		var k, v string
		_ = rows.Scan(&k, &v)
		if snapshot[k] != v {
			t.Fatalf("result for %s changed between identical runs", k)
		}
	}
	if before != 3 {
		t.Fatalf("expected 3 distinct results, got %d", before)
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM job_match_results WHERE user_id = $1 AND algorithm_version = 'match-v1'`, uid); n != 3 {
		t.Fatalf("results recorded with version: %d", n)
	}

	// Job detail: evidence-backed breakdown.
	var detail struct {
		Status string
		Match  struct {
			Score       float64
			Eligibility string
			Skills      []struct {
				Name          string `json:"name"`
				Status        string `json:"status"`
				ProfileSource string `json:"profile_source"`
				JobEvidence   string `json:"job_evidence"`
			} `json:"skills"`
			Explanation []string
		}
		Analysis struct {
			Method   string
			Embedded bool
		}
		DeepAvailable bool   `json:"deep_analysis_available"`
		Disclaimer    string `json:"disclaimer"`
	}
	c.Do(http.MethodGet, "/api/v1/jobs/"+jobs["a"]+"/match", nil).JSON(t, &detail)
	if detail.Status != "READY" || detail.Analysis.Method != "DETERMINISTIC" || !detail.Analysis.Embedded || detail.DeepAvailable {
		t.Fatalf("detail: %+v", detail)
	}
	statuses := map[string]string{}
	for _, s := range detail.Match.Skills {
		statuses[s.Name] = s.Status + "/" + s.ProfileSource
		if s.JobEvidence == "" {
			t.Fatalf("skill %s lacks job evidence", s.Name)
		}
	}
	if statuses["Go"] != "MATCHED/SKILL_VERIFIED" || statuses["Kafka"] != "MISSING/" || statuses["Terraform"] != "MISSING/" {
		t.Fatalf("skill statuses: %v", statuses)
	}
	if !strings.Contains(strings.Join(detail.Match.Explanation, " "), "Go (confirmed on your profile)") || detail.Disclaimer == "" {
		t.Fatalf("explanation: %v", detail.Match.Explanation)
	}

	// Discover list shows the viewer's scores and can sort by them.
	var list struct {
		Data []struct {
			ID    string `json:"id"`
			Match *struct {
				Score       float64
				Eligibility string
			} `json:"match"`
		}
	}
	c.Do(http.MethodGet, "/api/v1/jobs?sort=match", nil).JSON(t, &list)
	if len(list.Data) != 3 || list.Data[0].ID != jobs["a"] || list.Data[0].Match == nil {
		t.Fatalf("sort=match: %+v", list.Data)
	}

	// Skill gaps across top matches.
	var gaps struct {
		Data []struct {
			Skill string
			Jobs  int
		}
	}
	c.Do(http.MethodGet, "/api/v1/matches/skill-gaps", nil).JSON(t, &gaps)
	gapCount := map[string]int{}
	for _, g := range gaps.Data {
		gapCount[g.Skill] = g.Jobs
	}
	if len(gaps.Data) != 3 || gapCount["Kafka"] != 1 || gapCount["React"] != 1 || gapCount["Rust"] != 0 {
		t.Fatalf("gaps (ineligible jobs excluded): %+v", gaps.Data)
	}

	// Feedback: dismissing hides the job from the default list; it is validated.
	if r := c.Do(http.MethodPut, "/api/v1/jobs/"+jobs["a"]+"/match/feedback", map[string]any{"rating": "MEH"}); r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad rating: %d", r.Code)
	}
	r := c.Do(http.MethodPut, "/api/v1/jobs/"+jobs["a"]+"/match/feedback", map[string]any{"rating": "DOWN", "reason": "NOT_INTERESTED", "dismissed": true})
	if r.Code != http.StatusOK {
		t.Fatalf("feedback: %d %s", r.Code, r.Body)
	}
	c.Do(http.MethodGet, "/api/v1/matches", nil).JSON(t, &page)
	if page.Total != 1 || page.Data[0].Job.ID == jobs["a"] {
		t.Fatalf("dismissed job still listed: %+v", page)
	}
	c.Do(http.MethodGet, "/api/v1/matches?include_dismissed=true", nil).JSON(t, &page)
	if page.Total != 2 {
		t.Fatalf("include_dismissed: %d", page.Total)
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM match_feedback WHERE user_id = $1 AND score_at_feedback IS NOT NULL AND algorithm_version = 'match-v1'`, uid); n != 1 {
		t.Fatal("feedback must record the score and version it reacted to")
	}

	// Isolation: another user sees none of this.
	other := app.Client(t)
	other.Register("other@example.com")
	other.Do(http.MethodGet, "/api/v1/matches?eligibility=ALL&include_dismissed=true", nil).JSON(t, &page)
	if page.Total != 0 || page.State.Status != "NOT_STARTED" {
		t.Fatalf("cross-user leak: %+v", page)
	}
	var ofb struct{ Feedback *struct{ Rating string } }
	other.Do(http.MethodGet, "/api/v1/jobs/"+jobs["a"]+"/match", nil).JSON(t, &ofb)
	if ofb.Feedback != nil {
		t.Fatal("feedback leaked across users")
	}

	// A job that has not been analysed yet is reported as pending, not guessed.
	newID := ids.New()
	app.Exec(t, `INSERT INTO jobs (id, source_id, external_id, canonical_url, apply_url, title, company_name, remote_status, content_hash, dedupe_key)
		SELECT $1, source_id, 'z', canonical_url || 'z', apply_url, 'Go Engineer', company_name, 'REMOTE', decode('ab', 'hex'), 'k-new' FROM jobs LIMIT 1`, newID)
	c.Do(http.MethodGet, "/api/v1/jobs/"+newID.String()+"/match", nil).JSON(t, &early)
	if early.Status != "ANALYSIS_PENDING" {
		t.Fatalf("unanalysed job: %s", early.Status)
	}
	if r := c.Do(http.MethodGet, "/api/v1/jobs/"+uuid.NewString()+"/match", nil); r.Code != http.StatusNotFound {
		t.Fatalf("unknown job: %d", r.Code)
	}

	// Deep analysis is refused honestly without an LLM provider.
	if r := c.Do(http.MethodPost, "/api/v1/jobs/"+jobs["a"]+"/analysis", nil); r.Code != http.StatusServiceUnavailable || r.ErrCode() != "AI_PROVIDER_UNAVAILABLE" {
		t.Fatalf("deep analysis without provider: %d %s", r.Code, r.Body)
	}
}

func TestDeepAnalysisQueuedAndUsed(t *testing.T) {
	app, jobs := setupMatching(t, true)
	ctx := context.Background()
	c := app.Client(t)
	c.Register("deep@example.com")
	uid := userIDByEmail(t, app, "deep@example.com")
	buildProfile(t, c)

	r := c.Do(http.MethodPost, "/api/v1/jobs/"+jobs["a"]+"/analysis", nil)
	if r.Code != http.StatusAccepted {
		t.Fatalf("queue deep analysis: %d %s", r.Code, r.Body)
	}
	var q struct{ Queued bool }
	r.JSON(t, &q)
	if !q.Queued {
		t.Fatal("expected queued")
	}
	c.Do(http.MethodPost, "/api/v1/jobs/"+jobs["a"]+"/analysis", nil).JSON(t, &q)
	if q.Queued {
		t.Fatal("second request must be deduplicated")
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM job_analysis_requests WHERE status = 'QUEUED' AND requested_by = $1`, uid); n != 1 {
		t.Fatal("request not tracked")
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM audit_events WHERE action = 'job.analysis.deep_requested' AND user_id = $1`, uid); n != 2 {
		t.Fatalf("audit events = %d", n)
	}

	jobID, _ := ids.Parse(jobs["a"])
	if err := app.Matching.DeepAnalyze(ctx, jobID); err != nil {
		t.Fatal(err)
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM job_analyses WHERE method = 'LLM' AND model = 'fake-llm' AND input_tokens = 10`); n != 1 {
		t.Fatal("LLM analysis not stored with usage")
	}
	var detail struct {
		Status string
		Match  struct {
			AnalysisMethod string `json:"analysis_method"`
		}
		Analysis struct{ Method string }
	}
	c.Do(http.MethodGet, "/api/v1/jobs/"+jobs["a"]+"/match", nil).JSON(t, &detail)
	if detail.Analysis.Method != "LLM" || detail.Match.AnalysisMethod != "LLM" {
		t.Fatalf("LLM analysis should be preferred: %+v", detail)
	}
	// Already current: no new request.
	c.Do(http.MethodPost, "/api/v1/jobs/"+jobs["a"]+"/analysis", nil).JSON(t, &q)
	if q.Queued {
		t.Fatal("current analysis must not be re-queued")
	}
}
