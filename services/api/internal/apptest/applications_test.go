package apptest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/careerpilot/careerpilot/services/api/internal/audit"
	"github.com/careerpilot/careerpilot/services/api/internal/automation"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/sources"
)

type appDetail struct {
	ID             string   `json:"id"`
	Status         string   `json:"status"`
	Channel        *string  `json:"channel"`
	AllowedTargets []string `json:"allowed_transitions"`
	Pinned         struct {
		CV *string `json:"cv_version_id"`
	} `json:"pinned"`
	Package struct {
		Ready    bool     `json:"ready"`
		Blockers []string `json:"blockers"`
	} `json:"package"`
	Attempts []struct {
		ID, Status    string
		FailureReason *string `json:"failure_reason"`
	} `json:"attempts"`
	Evidence []struct{ Type, Reference string } `json:"evidence"`
	History  []struct {
		ToStatus string `json:"to_status"`
	} `json:"history"`
	SubmittedAt *string `json:"submitted_at"`
}

func setupApps(t *testing.T) (*App, *Client, uuid.UUID, []string) {
	t.Helper()
	fake := &fakeDocAI{llm: true}
	ai := httptest.NewServer(fake.handler())
	t.Cleanup(ai.Close)
	gh := newFake(sources.Greenhouse)
	app := New(t, Options{AIURL: ai.URL, Adapters: sources.Registry{sources.Greenhouse: gh}})
	src := addSource(t, app, sources.Greenhouse, "acme", 15)
	gh.set("acme",
		withText(posting("a", "Senior Backend Engineer", "Acme", "Remote - Worldwide"), "Requirements:\nGo"),
		withText(posting("b", "Platform Engineer", "Acme", "Remote - Worldwide"), "Requirements:\nGo"))
	if run, err := app.Discovery.RunSource(context.Background(), src, "MANUAL"); err != nil || run.Created != 2 {
		t.Fatalf("discovery: %+v %v", run, err)
	}
	var jobs []string
	rows, _ := app.Pool.Query(context.Background(), `SELECT id::text FROM jobs ORDER BY external_id`)
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		jobs = append(jobs, id)
	}
	rows.Close()
	c := app.Client(t)
	c.Register("apps@example.com")
	if r := c.Do(http.MethodPost, "/api/v1/profiles/me/experiences", map[string]any{"employer": "Acme", "title": "Engineer",
		"start_date": "2020-01-01", "is_current": true, "achievements": []string{"Shipped the billing API."}}); r.Code >= 300 {
		t.Fatalf("experience: %d", r.Code)
	}
	return app, c, userIDByEmail(t, app, "apps@example.com"), jobs
}

// approvedCV creates a CV from the profile and approves it; returns the document id.
func approvedCV(t *testing.T, c *Client, jobID string) string {
	t.Helper()
	var created struct{ Document struct{ ID string } }
	if r := c.Do(http.MethodPost, "/api/v1/jobs/"+jobID+"/application-documents", map[string]any{"kind": "CV", "source": "PROFILE"}); r.Code != 201 {
		t.Fatalf("cv: %d %s", r.Code, r.Body)
	} else {
		r.JSON(t, &created)
	}
	var v docView
	c.Do(http.MethodGet, "/api/v1/application-documents/"+created.Document.ID, nil).JSON(t, &v)
	if r := c.Do(http.MethodPost, "/api/v1/application-documents/"+created.Document.ID+"/approval", map[string]any{"version_id": v.Current.ID}); r.Code != 200 {
		t.Fatalf("approve cv: %d %s", r.Code, r.Body)
	}
	return created.Document.ID
}

func doKey(c *Client, path, key string) Response {
	return c.DoWithHeader(http.MethodPost, path, nil, map[string]string{"Idempotency-Key": key})
}

func TestApplicationPipelineAndAssistedSubmission(t *testing.T) {
	app, c, uid, jobs := setupApps(t)
	ctx := context.Background()
	base := "/api/v1/applications"

	var a appDetail
	r := c.Do(http.MethodPost, base, map[string]any{"job_id": jobs[0]})
	if r.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", r.Code, r.Body)
	}
	r.JSON(t, &a)
	if a.Status != "SHORTLISTED" || a.Package.Ready {
		t.Fatalf("created: %+v", a)
	}
	// Duplicate applications are refused with a pointer to the existing one.
	if r := c.Do(http.MethodPost, base, map[string]any{"job_id": jobs[0]}); r.Code != 409 || !strings.Contains(string(r.Body), a.ID) {
		t.Fatalf("duplicate create: %d %s", r.Code, r.Body)
	}
	path := base + "/" + a.ID
	move := func(to string) Response {
		return c.Do(http.MethodPost, path+"/transitions", map[string]any{"to": to})
	}
	// Invalid and guarded transitions are rejected.
	for _, to := range []string{"SUBMITTED", "APPROVED", "OFFER"} {
		if r := move(to); r.Code != 409 || r.ErrCode() != "INVALID_STATE_TRANSITION" {
			t.Fatalf("SHORTLISTED → %s: %d %s", to, r.Code, r.Body)
		}
	}
	if r := move("PREPARING"); r.Code != 200 {
		t.Fatalf("→ PREPARING: %d %s", r.Code, r.Body)
	}
	// Approval needs an approved CV.
	if r := move("AWAITING_APPROVAL"); r.Code != 422 || !strings.Contains(string(r.Body), "CV") {
		t.Fatalf("→ AWAITING_APPROVAL without CV: %d %s", r.Code, r.Body)
	}
	cvDoc := approvedCV(t, c, jobs[0])
	// An unapproved cover letter is reported and left out of the package.
	if r := c.Do(http.MethodPost, "/api/v1/jobs/"+jobs[0]+"/application-documents", map[string]any{"kind": "COVER_LETTER", "source": "PROFILE"}); r.Code != 201 {
		t.Fatalf("cover letter: %d %s", r.Code, r.Body)
	}
	if r := move("AWAITING_APPROVAL"); r.Code != 200 {
		t.Fatalf("→ AWAITING_APPROVAL: %d %s", r.Code, r.Body)
	} else if !strings.Contains(string(r.Body), "Cover letter is not approved and will not be included.") {
		t.Fatalf("missing package warning: %s", r.Body)
	}
	if r := c.Do(http.MethodPost, path+"/approval", map[string]any{"channel": "ASSISTED"}); r.Code != 422 {
		t.Fatalf("approve without consent: %d", r.Code)
	}
	if r := c.Do(http.MethodPost, path+"/approval", map[string]any{"channel": "EMAIL", "confirm": true}); r.Code != 422 || !strings.Contains(string(r.Body), "Gmail") {
		t.Fatalf("email channel: %d %s", r.Code, r.Body)
	}
	r = c.Do(http.MethodPost, path+"/approval", map[string]any{"channel": "ASSISTED", "confirm": true})
	if r.Code != 200 {
		t.Fatalf("approve: %d %s", r.Code, r.Body)
	}
	r.JSON(t, &a)
	if a.Status != "APPROVED" || a.Pinned.CV == nil || *a.Channel != "ASSISTED" {
		t.Fatalf("approved: %+v", a)
	}
	// A pinned document cannot be deleted.
	if r := c.Do(http.MethodDelete, "/api/v1/application-documents/"+cvDoc, nil); r.Code != 409 {
		t.Fatalf("delete pinned document: %d %s", r.Code, r.Body)
	}

	// Submissions need an idempotency key; replays return the same attempt.
	if r := c.Do(http.MethodPost, path+"/submissions", nil); r.Code != 422 {
		t.Fatalf("no key: %d", r.Code)
	}
	var first, replay struct {
		Attempt  struct{ ID, Status string }
		Replayed bool
	}
	if r := doKey(c, path+"/submissions", "key-attempt-1"); r.Code != 201 {
		t.Fatalf("start: %d %s", r.Code, r.Body)
	} else {
		r.JSON(t, &first)
	}
	doKey(c, path+"/submissions", "key-attempt-1").JSON(t, &replay)
	if replay.Attempt.ID != first.Attempt.ID || !replay.Replayed {
		t.Fatalf("replay: %+v vs %+v", replay, first)
	}
	if r := doKey(c, path+"/submissions", "key-attempt-2"); r.Code != 409 {
		t.Fatalf("second concurrent attempt: %d", r.Code)
	}
	// Agents cannot perform assisted submissions (ADR-007).
	_, _, err := app.Applications.StartSubmission(ctx, uid, uuid.MustParse(a.ID), "agent-key-1", audit.ActorAgent, "")
	var he *httpx.Error
	if !asHTTP(err, &he) || he.Code != httpx.CodeForbidden {
		t.Fatalf("agent start: %v", err)
	}

	// A failed attempt is recorded; the application becomes FAILED.
	r = c.Do(http.MethodPost, path+"/submissions/"+first.Attempt.ID+"/fail", map[string]any{"reason": "VERIFICATION_REQUIRED"})
	if r.Code != 200 {
		t.Fatalf("fail: %d %s", r.Code, r.Body)
	}
	r.JSON(t, &a)
	if a.Status != "FAILED" || a.Attempts[0].Status != "FAILED" || *a.Attempts[0].FailureReason != "VERIFICATION_REQUIRED" || a.SubmittedAt != nil {
		t.Fatalf("after failure: %+v", a)
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM audit_events WHERE action = 'application.submission.fail' AND outcome = 'FAILURE'`); n != 1 {
		t.Fatal("failure not audited")
	}

	// Retry: FAILED → APPROVED, new attempt, confirm with evidence.
	if r := move("APPROVED"); r.Code != 200 {
		t.Fatalf("retry: %d %s", r.Code, r.Body)
	}
	var second struct{ Attempt struct{ ID string } }
	doKey(c, path+"/submissions", "key-attempt-3").JSON(t, &second)
	confirm := path + "/submissions/" + second.Attempt.ID + "/confirm"
	if r := c.Do(http.MethodPost, confirm, map[string]any{}); r.Code != 422 {
		t.Fatalf("confirm without attestation: %d", r.Code)
	}
	if r := c.Do(http.MethodPost, confirm, map[string]any{"attest": true, "type": "ATS_CONFIRMATION"}); r.Code != 422 {
		t.Fatalf("user claiming ATS confirmation: %d", r.Code)
	}
	var shot struct{ ID string }
	c.Upload("/api/v1/documents", "confirmation.pdf", pdfBytes, "EVIDENCE").JSON(t, &shot)
	r = c.Do(http.MethodPost, confirm, map[string]any{"attest": true, "reference": "Greenhouse confirmation #A123", "document_id": shot.ID})
	if r.Code != 200 {
		t.Fatalf("confirm: %d %s", r.Code, r.Body)
	}
	r.JSON(t, &a)
	if a.Status != "SUBMITTED" || a.SubmittedAt == nil || len(a.Evidence) != 1 || a.Evidence[0].Type != "USER_ATTESTED" {
		t.Fatalf("submitted: %+v", a)
	}
	// No second submission once submitted.
	if r := doKey(c, path+"/submissions", "key-attempt-4"); r.Code != 409 {
		t.Fatalf("submit twice: %d", r.Code)
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM submission_attempts WHERE status = 'SUCCEEDED'`); n != 1 {
		t.Fatalf("successful attempts = %d", n)
	}

	// Outcome tracking and stats.
	for _, to := range []string{"INTERVIEWING", "OFFER"} {
		if r := move(to); r.Code != 200 {
			t.Fatalf("→ %s: %d", to, r.Code)
		}
	}
	var st struct {
		Submitted, Offers int
		ResponseRate      *float64 `json:"response_rate"`
	}
	c.Do(http.MethodGet, base+"/stats", nil).JSON(t, &st)
	if st.Submitted != 1 || st.Offers != 1 || st.ResponseRate == nil || *st.ResponseRate != 1 {
		t.Fatalf("stats: %+v", st)
	}
	var act struct {
		Data []struct {
			ToStatus string `json:"to_status"`
		}
	}
	c.Do(http.MethodGet, base+"/activity", nil).JSON(t, &act)
	if len(act.Data) == 0 || act.Data[0].ToStatus != "OFFER" {
		t.Fatalf("activity: %+v", act)
	}

	// History is append-only; submitted applications cannot be deleted.
	if _, err := app.Pool.Exec(ctx, `UPDATE application_status_history SET reason = 'x'`); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("history update not rejected: %v", err)
	}
	if r := c.Do(http.MethodDelete, path, nil); r.Code != 409 {
		t.Fatalf("delete submitted: %d", r.Code)
	}
	// Deleting the attached file unlinks it; the evidence record itself stays.
	if r := c.Do(http.MethodDelete, "/api/v1/documents/"+shot.ID, nil); r.Code != 204 {
		t.Fatalf("delete evidence file: %d %s", r.Code, r.Body)
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM submission_evidence WHERE document_id IS NULL AND reference <> ''`); n != 1 {
		t.Fatal("evidence lost or still linked after its file was deleted")
	}

	// Isolation.
	other := app.Client(t)
	other.Register("other-apps@example.com")
	if r := other.Do(http.MethodGet, path, nil); r.Code != 404 {
		t.Fatalf("cross-user read: %d", r.Code)
	}
	if r := doKey(other, path+"/submissions", "other-key-1"); r.Code != 404 {
		t.Fatalf("cross-user submit: %d", r.Code)
	}
}

func asHTTP(err error, target **httpx.Error) bool {
	for err != nil {
		if he, ok := err.(*httpx.Error); ok {
			*target = he
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func TestAutomationControls(t *testing.T) {
	app, c, uid, jobs := setupApps(t)
	ctx := context.Background()
	guard := automation.NewGuard(app.Pool)

	var s struct {
		Mode   string
		Paused bool
		Limit  int `json:"daily_submission_limit"`
	}
	c.Do(http.MethodGet, "/api/v1/automation/settings", nil).JSON(t, &s)
	if s.Mode != "HUMAN_APPROVED" || s.Paused || s.Limit != 20 {
		t.Fatalf("defaults: %+v", s)
	}
	// Agents may not submit in human-approved mode.
	if err := guard.Authorize(ctx, app.Pool, uid, audit.ActorAgent, automation.ActionSubmit); err == nil {
		t.Fatal("agent submit allowed in HUMAN_APPROVED mode")
	}
	if r := c.Do(http.MethodPut, "/api/v1/automation/settings", map[string]any{"mode": "CONTROLLED", "daily_submission_limit": 1}); r.Code != 200 {
		t.Fatalf("update: %d %s", r.Code, r.Body)
	}
	if err := guard.Authorize(ctx, app.Pool, uid, audit.ActorAgent, automation.ActionPrepare); err != nil {
		t.Fatalf("agent prepare in CONTROLLED: %v", err)
	}
	// The kill switch stops agents but never the user acting themselves.
	c.Do(http.MethodPost, "/api/v1/automation/pause", nil).JSON(t, &s)
	if !s.Paused {
		t.Fatal("not paused")
	}
	err := guard.Authorize(ctx, app.Pool, uid, audit.ActorAgent, automation.ActionPrepare)
	if d, ok := err.(*automation.Denied); !ok || d.Reason != "PAUSED" {
		t.Fatalf("paused agent: %v", err)
	}
	if err := guard.Authorize(ctx, app.Pool, uid, audit.ActorUser, automation.ActionPrepare); err != nil {
		t.Fatalf("user blocked by pause: %v", err)
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM audit_events WHERE outcome = 'DENIED' AND subject_type = 'automation'`); n < 2 {
		t.Fatalf("denials not audited: %d", n)
	}

	// The daily limit applies to everyone: one submission, then the second is refused.
	for i, job := range jobs {
		var a appDetail
		c.Do(http.MethodPost, "/api/v1/applications", map[string]any{"job_id": job}).JSON(t, &a)
		path := "/api/v1/applications/" + a.ID
		c.Do(http.MethodPost, path+"/transitions", map[string]any{"to": "PREPARING"})
		approvedCV(t, c, job)
		c.Do(http.MethodPost, path+"/transitions", map[string]any{"to": "AWAITING_APPROVAL"})
		if r := c.Do(http.MethodPost, path+"/approval", map[string]any{"channel": "ASSISTED", "confirm": true}); r.Code != 200 {
			t.Fatalf("approve %d: %d %s", i, r.Code, r.Body)
		}
		r := doKey(c, path+"/submissions", fmt.Sprintf("limit-key-%d", i))
		if i == 0 && r.Code != 201 {
			t.Fatalf("first submission: %d %s", r.Code, r.Body)
		}
		if i == 1 && (r.Code != 429 || !strings.Contains(string(r.Body), "limit")) {
			t.Fatalf("over limit: %d %s", r.Code, r.Body)
		}
		if i == 0 {
			// Revoking the approval cancels the attempt in flight.
			if r := c.Do(http.MethodDelete, path+"/approval", nil); r.Code != 200 {
				t.Fatalf("revoke: %d %s", r.Code, r.Body)
			}
			if n := app.QueryInt(t, `SELECT count(*) FROM submission_attempts WHERE status = 'CANCELLED'`); n != 1 {
				t.Fatal("revoke did not cancel the pending attempt")
			}
			// Cancelled attempts do not count towards the limit; record a real one.
			c.Do(http.MethodPost, path+"/approval", map[string]any{"channel": "ASSISTED", "confirm": true})
			if r := doKey(c, path+"/submissions", "limit-key-again"); r.Code != 201 {
				t.Fatalf("restart: %d %s", r.Code, r.Body)
			}
		}
	}
	if r := c.Do(http.MethodPost, "/api/v1/automation/resume", nil); r.Code != 200 {
		t.Fatalf("resume: %d", r.Code)
	}
	if r := c.Do(http.MethodPut, "/api/v1/automation/settings", map[string]any{"mode": "YOLO", "daily_submission_limit": 0}); r.Code != 422 {
		t.Fatalf("invalid settings: %d", r.Code)
	}
}
