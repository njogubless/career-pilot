package apptest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"github.com/careerpilot/careerpilot/services/api/internal/jobs"
	"github.com/careerpilot/careerpilot/services/api/internal/preparation"
	"github.com/careerpilot/careerpilot/services/api/internal/sources"
)

// fakeDocAI implements the AI service's document endpoints: generation builds
// a CV from the facts it receives; validation flags unknown refs, the figure
// "99%" and unanswered questions.
type fakeDocAI struct {
	llm      bool
	mu       sync.Mutex
	gotFacts []map[string]any
}

type fakeFact struct {
	Ref, Kind, Text, Parent string
}

func (f *fakeDocAI) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /internal/v1/info", func(w http.ResponseWriter, r *http.Request) {
		caps := []string{"info", "documents.validate"}
		if f.llm {
			caps = append(caps, "documents.generate")
		}
		writeJSON(w, map[string]any{"llm_configured": f.llm, "llm_provider": "fake", "capabilities": caps})
	})
	mux.HandleFunc("POST /internal/v1/documents/generate", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Kind  string
			Facts []fakeFact
		}
		raw := map[string]any{}
		body, _ := readAll(r)
		_ = json.Unmarshal(body, &in)
		_ = json.Unmarshal(body, &raw)
		f.mu.Lock()
		for _, x := range raw["facts"].([]any) {
			f.gotFacts = append(f.gotFacts, x.(map[string]any))
		}
		f.mu.Unlock()
		var exp, ach string
		skills := []string{}
		for _, x := range in.Facts {
			switch {
			case x.Kind == "experience" && exp == "":
				exp = x.Ref
			case x.Kind == "achievement" && ach == "":
				ach = x.Ref + "|" + x.Text
			case x.Kind == "skill":
				skills = append(skills, x.Ref)
			}
		}
		achRef, achText, _ := strings.Cut(ach, "|")
		content := map[string]any{"schema_version": "doc-v1", "kind": in.Kind, "sections": []any{
			map[string]any{"key": "contact", "title": "Contact", "blocks": []any{map[string]any{"id": "b1", "type": "contact"}}},
			map[string]any{"key": "experience", "title": "Experience", "blocks": []any{
				map[string]any{"id": "b2", "type": "entry", "refs": []string{exp}},
				map[string]any{"id": "b3", "type": "bullet", "text": achText, "refs": []string{achRef}, "meta": map[string]string{"parent": "b2"}},
			}},
			map[string]any{"key": "skills", "title": "Skills", "blocks": []any{map[string]any{"id": "b4", "type": "skills", "refs": skills}}},
		}}
		writeJSON(w, map[string]any{"content": content,
			"validation": map[string]any{"validator_version": "fake-check", "status": "PASSED", "issues": []any{}, "claims_checked": 1},
			"questions":  []string{"What was the team size?"}, "warnings": []string{},
			"usage": map[string]any{"provider": "fake", "model": "fake-deterministic", "prompt_version": "cv_tailor@1",
				"input_tokens": 10, "output_tokens": 5}})
	})
	mux.HandleFunc("POST /internal/v1/documents/validate", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Content preparation.Content
			Facts   []fakeFact
		}
		body, _ := readAll(r)
		_ = json.Unmarshal(body, &in)
		known := map[string]bool{}
		for _, x := range in.Facts {
			known[x.Ref] = true
		}
		issues := []map[string]string{}
		for _, s := range in.Content.Sections {
			for _, b := range s.Blocks {
				for _, ref := range b.Refs {
					if !known[ref] {
						issues = append(issues, map[string]string{"block_id": b.ID, "code": "UNKNOWN_REF", "severity": "ERROR", "message": "unknown"})
					}
				}
				if strings.Contains(b.Text, "99%") {
					issues = append(issues, map[string]string{"block_id": b.ID, "code": "UNSUPPORTED_NUMBER", "severity": "ERROR", "message": "99"})
				}
				if b.Meta["status"] == "NEEDS_INPUT" {
					issues = append(issues, map[string]string{"block_id": b.ID, "code": "NEEDS_INPUT", "severity": "ERROR", "message": "answer"})
				}
			}
		}
		status := "PASSED"
		if len(issues) > 0 {
			status = "FAILED"
		}
		writeJSON(w, map[string]any{"validator_version": "fake-check", "status": status, "issues": issues, "claims_checked": 1})
	})
	return mux
}

func readAll(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return []byte(b.String()), nil
}

func setupPrep(t *testing.T, llm bool) (*App, *fakeDocAI, string) {
	t.Helper()
	fake := &fakeDocAI{llm: llm}
	ai := httptest.NewServer(fake.handler())
	t.Cleanup(ai.Close)
	gh := newFake(sources.Greenhouse)
	app := New(t, Options{AIURL: ai.URL, Adapters: sources.Registry{sources.Greenhouse: gh}})
	src := addSource(t, app, sources.Greenhouse, "acme", 15)
	gh.set("acme", withText(posting("a", "Senior Backend Engineer", "Acme Pay", "Remote - Worldwide"), "Requirements:\nGo"))
	if run, err := app.Discovery.RunSource(context.Background(), src, "MANUAL"); err != nil || run.Created != 1 {
		t.Fatalf("discovery: %+v %v", run, err)
	}
	var jobID string
	if err := app.Pool.QueryRow(context.Background(), `SELECT id::text FROM jobs`).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	return app, fake, jobID
}

func startPrepWorker(t *testing.T, app *App) {
	t.Helper()
	workers := river.NewWorkers()
	river.AddWorker(workers, &preparation.GenerateDocumentWorker{Service: app.Preparation})
	wc, err := jobs.NewWorkerClient(app.Pool, workers, app.Log, 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := wc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = wc.Stop(context.Background())
	})
}

type docView struct {
	ID               string `json:"id"`
	Kind             string `json:"kind"`
	ApprovalState    string `json:"approval_state"`
	CurrentVersionID string `json:"current_version_id"`
	Current          *struct {
		ID               string              `json:"id"`
		VersionNo        int                 `json:"version_no"`
		Origin           string              `json:"origin"`
		ValidationStatus string              `json:"validation_status"`
		Content          preparation.Content `json:"content"`
		Questions        []string            `json:"questions"`
		FactsChanged     bool                `json:"facts_changed"`
	} `json:"current_version"`
	Generation *struct {
		Status    string  `json:"status"`
		ErrorCode *string `json:"error_code"`
	} `json:"latest_generation"`
}

func waitGeneration(t *testing.T, c *Client, docID string) docView {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var v docView
	for {
		c.Do(http.MethodGet, "/api/v1/application-documents/"+docID, nil).JSON(t, &v)
		if v.Generation != nil && (v.Generation.Status == "SUCCEEDED" || v.Generation.Status == "FAILED") {
			return v
		}
		if time.Now().After(deadline) {
			t.Fatalf("generation did not finish: %+v", v.Generation)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestApplicationDocumentLifecycle(t *testing.T) {
	app, fake, jobID := setupPrep(t, true)
	c := app.Client(t)
	c.Register("prep@example.com")
	for _, step := range []struct {
		path string
		body any
	}{
		{"/api/v1/profiles/me/experiences", map[string]any{"employer": "Acme", "title": "Senior Engineer", "start_date": "2020-01-01",
			"is_current": true, "achievements": []string{"Cut ledger costs by 30%."}}},
		{"/api/v1/profiles/me/skills", map[string]any{"name": "Go"}},
		{"/api/v1/profiles/me/skills", map[string]any{"name": "Kubernetes"}},
	} {
		if r := c.Do(http.MethodPost, step.path, step.body); r.Code >= 300 {
			t.Fatalf("%s: %d %s", step.path, r.Code, r.Body)
		}
	}
	app.Exec(t, `UPDATE profile_skills SET verification = 'UNVERIFIED', source = 'CV_IMPORT' WHERE name = 'Kubernetes'`)

	// Answers need questions; unknown kinds are rejected.
	if r := c.Do(http.MethodPost, "/api/v1/jobs/"+jobID+"/application-documents", map[string]any{"kind": "APPLICATION_ANSWERS"}); r.Code != 422 {
		t.Fatalf("answers without questions: %d", r.Code)
	}
	if r := c.Do(http.MethodPost, "/api/v1/jobs/"+jobID+"/application-documents", map[string]any{"kind": "MEMO"}); r.Code != 422 {
		t.Fatalf("bad kind: %d", r.Code)
	}

	r := c.Do(http.MethodPost, "/api/v1/jobs/"+jobID+"/application-documents", map[string]any{"kind": "CV", "notes": "I use Acme Pay daily."})
	if r.Code != http.StatusAccepted {
		t.Fatalf("generate: %d %s", r.Code, r.Body)
	}
	var started struct {
		Document struct{ ID string }
	}
	r.JSON(t, &started)
	docID := started.Document.ID
	if n := app.QueryInt(t, `SELECT count(*) FROM river_job WHERE kind = 'generate_application_document'`); n != 1 {
		t.Fatalf("generation not enqueued transactionally: %d", n)
	}
	if r := c.Do(http.MethodPost, "/api/v1/jobs/"+jobID+"/application-documents", map[string]any{"kind": "CV"}); r.Code != http.StatusConflict {
		t.Fatalf("second concurrent generation: %d", r.Code)
	}

	startPrepWorker(t, app)
	v := waitGeneration(t, c, docID)
	if v.Generation.Status != "SUCCEEDED" || v.Current == nil {
		t.Fatalf("generation: %+v; document: %s", v.Generation, c.Do(http.MethodGet, "/api/v1/application-documents/"+docID, nil).Body)
	}
	if v.Current.Origin != "GENERATED" || v.Current.VersionNo != 1 || v.ApprovalState != "NONE" || v.Current.FactsChanged {
		t.Fatalf("version 1: %+v state=%s", v.Current, v.ApprovalState)
	}
	if len(v.Current.Questions) != 1 {
		t.Fatalf("questions for the user not stored: %v", v.Current.Questions)
	}
	// Only verified facts reach the AI service; the user's note is a citable fact.
	kinds := map[string]bool{}
	for _, f := range fake.gotFacts {
		if f["text"] == "Kubernetes" {
			t.Fatal("unverified skill was sent to the generator")
		}
		kinds[f["kind"].(string)] = true
	}
	if !kinds["note"] || !kinds["achievement"] {
		t.Fatalf("facts sent: %v", fake.gotFacts)
	}
	// Structural text is rendered by the platform from the profile.
	exp := v.Current.Content.Sections[1]
	if exp.Blocks[0].Text != "Senior Engineer — Acme" || exp.Blocks[0].Meta["dates"] != "Jan 2020 – Present" {
		t.Fatalf("entry not rendered from profile: %+v", exp.Blocks[0])
	}
	if v.Current.Content.Sections[0].Blocks[0].Text != "Test User" || v.Current.Content.Sections[2].Blocks[0].Text != "Go" {
		t.Fatalf("contact/skills not rendered: %+v", v.Current.Content.Sections)
	}
	v1 := v.Current.ID

	// Edit: an unsupported figure is saved but flagged; a forged entry heading is re-rendered.
	edited := v.Current.Content
	edited.Sections[1].Blocks[1].Text = "Cut ledger costs by 99%."
	edited.Sections[1].Blocks[0].Text = "CEO — Google"
	var v2 struct {
		ID               string
		VersionNo        int `json:"version_no"`
		Origin           string
		ValidationStatus string `json:"validation_status"`
		Content          preparation.Content
	}
	r = c.Do(http.MethodPut, "/api/v1/application-documents/"+docID+"/content", map[string]any{"base_version_id": v1, "content": edited})
	if r.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", r.Code, r.Body)
	}
	r.JSON(t, &v2)
	if v2.VersionNo != 2 || v2.Origin != "USER_EDIT" || v2.ValidationStatus != "FAILED" {
		t.Fatalf("v2: %+v", v2)
	}
	if v2.Content.Sections[1].Blocks[0].Text != "Senior Engineer — Acme" {
		t.Fatal("user edit changed a profile-rendered entry")
	}
	// Saving identical content is idempotent; a stale base is a conflict.
	var same struct{ ID string }
	c.Do(http.MethodPut, "/api/v1/application-documents/"+docID+"/content", map[string]any{"base_version_id": v2.ID, "content": v2.Content}).JSON(t, &same)
	if same.ID != v2.ID {
		t.Fatal("identical save created a new version")
	}
	if r := c.Do(http.MethodPut, "/api/v1/application-documents/"+docID+"/content", map[string]any{"base_version_id": v1, "content": edited}); r.Code != http.StatusConflict {
		t.Fatalf("stale edit: %d", r.Code)
	}

	// Approval: unresolved errors need explicit acknowledgement; only the current version.
	if r := c.Do(http.MethodPost, "/api/v1/application-documents/"+docID+"/approval", map[string]any{"version_id": v1}); r.Code != http.StatusConflict {
		t.Fatalf("approve old version: %d", r.Code)
	}
	r = c.Do(http.MethodPost, "/api/v1/application-documents/"+docID+"/approval", map[string]any{"version_id": v2.ID})
	if r.Code != 422 || r.ErrCode() != "VALIDATION_FAILED" || !strings.Contains(string(r.Body), `"UNSUPPORTED_NUMBER"`) {
		t.Fatalf("approve with errors: %d %s", r.Code, r.Body)
	}
	r = c.Do(http.MethodPost, "/api/v1/application-documents/"+docID+"/approval", map[string]any{"version_id": v2.ID, "acknowledge_issues": true})
	if r.Code != http.StatusOK {
		t.Fatalf("approve with acknowledgement: %d %s", r.Code, r.Body)
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM application_document_approvals WHERE jsonb_array_length(acknowledged_issues) = 1 AND revoked_at IS NULL`); n != 1 {
		t.Fatal("acknowledged issues not recorded with the approval")
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM audit_events WHERE action = 'application_document.approve'`); n != 1 {
		t.Fatal("approval not audited")
	}

	// Restore v1: a new version; the approval now refers to an older version.
	r = c.Do(http.MethodPost, "/api/v1/application-documents/"+docID+"/versions/"+v1+"/restore", nil)
	if r.Code != http.StatusOK {
		t.Fatalf("restore: %d %s", r.Code, r.Body)
	}
	c.Do(http.MethodGet, "/api/v1/application-documents/"+docID, nil).JSON(t, &v)
	if v.Current.VersionNo != 3 || v.Current.Origin != "RESTORED" || v.ApprovalState != "OUTDATED" {
		t.Fatalf("after restore: %+v %s", v.Current, v.ApprovalState)
	}
	var versions struct {
		Data []struct {
			VersionNo int `json:"version_no"`
		}
	}
	c.Do(http.MethodGet, "/api/v1/application-documents/"+docID+"/versions", nil).JSON(t, &versions)
	if len(versions.Data) != 3 || versions.Data[0].VersionNo != 3 {
		t.Fatalf("versions: %+v", versions)
	}
	// Versions are immutable at the database level.
	if _, err := app.Pool.Exec(context.Background(), `UPDATE application_document_versions SET content = '{}'::jsonb`); err == nil ||
		!strings.Contains(err.Error(), "immutable") {
		t.Fatalf("version update was not rejected: %v", err)
	}

	// Export.
	r = c.Do(http.MethodGet, "/api/v1/application-documents/"+docID+"/versions/"+v.Current.ID+"/export?format=docx", nil)
	if r.Code != 200 || !strings.Contains(r.Header.Get("Content-Type"), "wordprocessingml") || string(r.Body[:2]) != "PK" ||
		!strings.Contains(r.Header.Get("Content-Disposition"), "tailored-cv-acme-pay-v3.docx") {
		t.Fatalf("docx export: %d %v", r.Code, r.Header)
	}
	r = c.Do(http.MethodGet, "/api/v1/application-documents/"+docID+"/versions/"+v.Current.ID+"/export?format=txt", nil)
	if r.Code != 200 || !strings.Contains(string(r.Body), "Senior Engineer — Acme") {
		t.Fatalf("txt export: %d %s", r.Code, r.Body)
	}
	if r := c.Do(http.MethodGet, "/api/v1/application-documents/"+docID+"/versions/"+v.Current.ID+"/export?format=pdf", nil); r.Code != 422 {
		t.Fatalf("pdf export: %d", r.Code)
	}

	// Profile changes mark stored validation as possibly stale; re-check uses the current profile.
	if r := c.Do(http.MethodPost, "/api/v1/profiles/me/skills", map[string]any{"name": "PostgreSQL"}); r.Code >= 300 {
		t.Fatal(r.Code)
	}
	c.Do(http.MethodGet, "/api/v1/application-documents/"+docID, nil).JSON(t, &v)
	if !v.Current.FactsChanged {
		t.Fatal("facts_changed not reported after a profile change")
	}
	var re struct{ Status string }
	if r := c.Do(http.MethodPost, "/api/v1/application-documents/"+docID+"/recheck", nil); r.Code != 200 {
		t.Fatalf("recheck: %d %s", r.Code, r.Body)
	} else {
		r.JSON(t, &re)
	}
	if re.Status != "PASSED" {
		t.Fatalf("recheck status = %s", re.Status)
	}

	// Isolation: another user sees nothing.
	other := app.Client(t)
	other.Register("other-prep@example.com")
	for _, path := range []string{"/api/v1/application-documents/" + docID, "/api/v1/application-documents/" + docID + "/versions",
		"/api/v1/application-documents/" + docID + "/versions/" + v1 + "/export?format=txt"} {
		if r := other.Do(http.MethodGet, path, nil); r.Code != http.StatusNotFound {
			t.Fatalf("cross-user %s: %d", path, r.Code)
		}
	}
	if r := other.Do(http.MethodPut, "/api/v1/application-documents/"+docID+"/content", map[string]any{"base_version_id": v1, "content": edited}); r.Code != 404 {
		t.Fatalf("cross-user edit: %d", r.Code)
	}
	var list struct{ Data []any }
	other.Do(http.MethodGet, "/api/v1/application-documents", nil).JSON(t, &list)
	if len(list.Data) != 0 {
		t.Fatal("cross-user list leaked documents")
	}

	// Revoke approval, then delete.
	if r := c.Do(http.MethodDelete, "/api/v1/application-documents/"+docID+"/approval", nil); r.Code != 200 {
		t.Fatalf("revoke: %d", r.Code)
	}
	if r := c.Do(http.MethodDelete, "/api/v1/application-documents/"+docID+"/approval", nil); r.Code != http.StatusConflict {
		t.Fatalf("second revoke: %d", r.Code)
	}
	if r := c.Do(http.MethodDelete, "/api/v1/application-documents/"+docID, nil); r.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Code, r.Body)
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM application_document_versions`); n != 0 {
		t.Fatal("versions not deleted with their document")
	}
}

func TestApplicationDocumentHonestFailures(t *testing.T) {
	// No LLM: nothing is created and the reason is explicit.
	app, _, jobID := setupPrep(t, false)
	c := app.Client(t)
	c.Register("nollm@example.com")
	r := c.Do(http.MethodPost, "/api/v1/jobs/"+jobID+"/application-documents", map[string]any{"kind": "CV"})
	if r.Code != 503 || r.ErrCode() != "AI_PROVIDER_UNAVAILABLE" {
		t.Fatalf("no llm: %d %s", r.Code, r.Body)
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM application_documents`); n != 0 {
		t.Fatal("document created although generation cannot run")
	}
	// Without an LLM the user can still start from their verified profile,
	// but an empty profile cannot produce a CV.
	if r := c.Do(http.MethodPost, "/api/v1/jobs/"+jobID+"/application-documents", map[string]any{"kind": "CV", "source": "PROFILE"}); r.Code != 422 {
		t.Fatalf("empty profile template: %d %s", r.Code, r.Body)
	}
	if r := c.Do(http.MethodPost, "/api/v1/profiles/me/experiences", map[string]any{"employer": "Acme", "title": "Engineer",
		"start_date": "2021-05-01", "is_current": true, "achievements": []string{"Shipped the billing API."}}); r.Code >= 300 {
		t.Fatalf("experience: %d", r.Code)
	}
	r = c.Do(http.MethodPost, "/api/v1/jobs/"+jobID+"/application-documents", map[string]any{"kind": "CV", "source": "PROFILE"})
	if r.Code != http.StatusCreated {
		t.Fatalf("from profile: %d %s", r.Code, r.Body)
	}
	var created struct{ Document struct{ ID string } }
	r.JSON(t, &created)
	var tv docView
	c.Do(http.MethodGet, "/api/v1/application-documents/"+created.Document.ID, nil).JSON(t, &tv)
	if tv.Current == nil || tv.Current.Origin != "TEMPLATE" || tv.Current.ValidationStatus != "PASSED" {
		t.Fatalf("template version: %+v", tv.Current)
	}
	if exp := tv.Current.Content.Sections[1]; exp.Blocks[0].Text != "Engineer — Acme" || exp.Blocks[1].Text != "Shipped the billing API." {
		t.Fatalf("template content: %+v", exp)
	}
	if r := c.Do(http.MethodPost, "/api/v1/jobs/"+jobID+"/application-documents", map[string]any{"kind": "CV", "source": "MAGIC"}); r.Code != 422 {
		t.Fatalf("bad source: %d", r.Code)
	}

	// LLM available but no verified facts: the generation fails with a clear reason.
	app2, _, job2 := setupPrep(t, true)
	c2 := app2.Client(t)
	c2.Register("empty@example.com")
	if r := c2.Do(http.MethodPost, "/api/v1/jobs/00000000-0000-0000-0000-000000000000/application-documents", map[string]any{"kind": "CV"}); r.Code != 404 {
		t.Fatalf("unknown job: %d", r.Code)
	}
	var started struct{ Document struct{ ID string } }
	c2.Do(http.MethodPost, "/api/v1/jobs/"+job2+"/application-documents", map[string]any{"kind": "COVER_LETTER"}).JSON(t, &started)
	startPrepWorker(t, app2)
	v := waitGeneration(t, c2, started.Document.ID)
	if v.Generation.Status != "FAILED" || v.Generation.ErrorCode == nil || *v.Generation.ErrorCode != "PROFILE_INCOMPLETE" || v.Current != nil {
		t.Fatalf("empty profile generation: %+v", v.Generation)
	}
}
