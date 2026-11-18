package apptest

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

type scheduleOut struct {
	ID          string
	NextRunAt   time.Time `json:"next_run_at"`
	LastOutcome *string   `json:"last_outcome"`
}

func TestScheduledWorkflowsRunReliably(t *testing.T) {
	app, c, _ := setupWorkflow(t, nil)
	buildProfile(t, c)
	ctx := context.Background()
	uid := userIDByEmail(t, app, "agent@example.com")

	body := map[string]any{"name": "Morning search", "objective": "Remote backend jobs", "template": "DISCOVER_AND_RANK",
		"params": map[string]any{"query": "backend", "remote_only": true, "min_score": 1}, "cadence": "DAILY", "hour": 7, "timezone": "Africa/Nairobi"}
	var s scheduleOut
	r := c.Do(http.MethodPost, "/api/v1/workflow-schedules", body)
	if r.Code != http.StatusCreated {
		t.Fatalf("create schedule: %d %s", r.Code, r.Body)
	}
	r.JSON(t, &s)
	if !s.NextRunAt.After(time.Now()) {
		t.Fatalf("next run not in the future: %s", s.NextRunAt)
	}
	if r := c.Do(http.MethodPost, "/api/v1/workflow-schedules", map[string]any{"name": "x", "template": "DISCOVER_AND_RANK", "cadence": "DAILY",
		"hour": 7, "timezone": "Nowhere/Land"}); r.Code != 422 {
		t.Fatalf("bad timezone: %d", r.Code)
	}

	// Due → exactly one run, tied to the schedule; the schedule advances.
	app.Exec(t, `UPDATE workflow_schedules SET next_run_at = date_trunc('minute', now()) - interval '1 minute' WHERE id = $1`, s.ID)
	if n, err := app.Workflows.RunDueSchedules(ctx); err != nil || n != 1 {
		t.Fatalf("run due: %d %v", n, err)
	}
	if n, err := app.Workflows.RunDueSchedules(ctx); err != nil || n != 0 {
		t.Fatalf("second sweep started again: %d %v", n, err)
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM workflow_executions WHERE schedule_id = $1`, s.ID); n != 1 {
		t.Fatalf("runs = %d", n)
	}
	var slot time.Time
	_ = app.Pool.QueryRow(ctx, `SELECT scheduled_for FROM workflow_executions WHERE schedule_id = $1`, s.ID).Scan(&slot)

	// Never overlaps its own active run.
	app.Exec(t, `UPDATE workflow_schedules SET next_run_at = now() - interval '1 minute' WHERE id = $1`, s.ID)
	app.Workflows.RunDueSchedules(ctx)
	var outcome string
	_ = app.Pool.QueryRow(ctx, `SELECT last_outcome FROM workflow_schedules WHERE id = $1`, s.ID).Scan(&outcome)
	if outcome != "SKIPPED_OVERLAP" {
		t.Fatalf("overlap outcome = %s", outcome)
	}
	// A double fire of the same slot cannot create a second run.
	app.Exec(t, `UPDATE workflow_executions SET status = 'CANCELLED' WHERE schedule_id = $1`, s.ID)
	app.Exec(t, `UPDATE workflow_schedules SET next_run_at = $2 WHERE id = $1`, s.ID, slot)
	app.Workflows.RunDueSchedules(ctx)
	if n := app.QueryInt(t, `SELECT count(*) FROM workflow_executions WHERE schedule_id = $1`, s.ID); n != 1 {
		t.Fatalf("duplicate run for the same slot: %d", n)
	}

	// The kill switch skips runs (and notifies once per slot).
	c.Do(http.MethodPost, "/api/v1/automation/pause", nil)
	app.Exec(t, `UPDATE workflow_schedules SET next_run_at = date_trunc('minute', now()) - interval '2 minutes' WHERE id = $1`, s.ID)
	var pausedSlot time.Time
	_ = app.Pool.QueryRow(ctx, `SELECT next_run_at FROM workflow_schedules WHERE id = $1`, s.ID).Scan(&pausedSlot)
	app.Workflows.RunDueSchedules(ctx)
	app.Exec(t, `UPDATE workflow_schedules SET next_run_at = $2 WHERE id = $1`, s.ID, pausedSlot)
	app.Workflows.RunDueSchedules(ctx)
	if n := app.QueryInt(t, `SELECT count(*) FROM workflow_executions WHERE schedule_id = $1`, s.ID); n != 1 {
		t.Fatalf("ran while paused: %d", n)
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM notifications WHERE user_id = $1 AND kind = 'SCHEDULE_SKIPPED'`, uid); n != 1 {
		t.Fatalf("skip notifications = %d", n)
	}
	c.Do(http.MethodPost, "/api/v1/automation/resume", nil)

	// Limit of 5 schedules.
	for i := 0; i < 4; i++ {
		body["name"] = fmt.Sprintf("Search %d", i)
		c.Do(http.MethodPost, "/api/v1/workflow-schedules", body)
	}
	if r := c.Do(http.MethodPost, "/api/v1/workflow-schedules", body); r.Code != 409 {
		t.Fatalf("schedule limit: %d", r.Code)
	}
	// Isolation.
	other := app.Client(t)
	other.Register("sched-other@example.com")
	if r := other.Do(http.MethodDelete, "/api/v1/workflow-schedules/"+s.ID, nil); r.Code != 404 {
		t.Fatalf("cross-user delete: %d", r.Code)
	}
	if r := c.Do(http.MethodDelete, "/api/v1/workflow-schedules/"+s.ID, nil); r.Code != 204 {
		t.Fatalf("delete: %d", r.Code)
	}
}

type notifList struct {
	Data []struct {
		ID, Kind, Title, Link string
		ReadAt                *time.Time `json:"read_at"`
	}
	Unread int `json:"unread_count"`
}

func TestNotificationsAreDerivedOnce(t *testing.T) {
	app, c, _ := setupWorkflow(t, nil)
	buildProfile(t, c)
	ctx := context.Background()
	uid := userIDByEmail(t, app, "agent@example.com")
	c.Do(http.MethodPut, "/api/v1/notifications/preferences", map[string]any{"high_match": true, "min_match_score": 50, "approvals": true,
		"workflow_events": true, "replies": true, "follow_ups": true, "weekly_report": true})
	startWorkflowWorker(t, app)
	id := startWorkflow(t, c, "DISCOVER_PREPARE_APPROVE", map[string]any{"query": "backend", "remote_only": true, "count": 2, "min_score": 1})
	waitWorkflow(t, c, id, "AWAITING_APPROVAL")

	n1, err := app.Notifications.Sweep(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n2, err := app.Notifications.Sweep(ctx)
	if err != nil || n2 != 0 {
		t.Fatalf("second sweep created %d (err %v): duplicates", n2, err)
	}
	var l notifList
	c.Do(http.MethodGet, "/api/v1/notifications", nil).JSON(t, &l)
	kinds := map[string]int{}
	for _, n := range l.Data {
		kinds[n.Kind]++
	}
	if kinds["APPROVAL_REQUESTED"] != 1 || kinds["HIGH_MATCH"] == 0 || l.Unread != n1 || len(l.Data) != n1 {
		t.Fatalf("notifications: %+v (created %d)", l, n1)
	}
	for _, n := range l.Data {
		if n.Kind == "APPROVAL_REQUESTED" && !strings.HasPrefix(n.Link, "/intelligence/workflows/") {
			t.Fatalf("link: %s", n.Link)
		}
	}
	// High-match notifications are capped per day; the rest go into one digest.
	for i := 0; i < 5; i++ {
		app.Exec(t, `INSERT INTO notifications (id, user_id, kind, dedupe_key, title) VALUES (gen_random_uuid(), $1, 'HIGH_MATCH', $2, 'x')`,
			uid, fmt.Sprintf("match:fake-%d", i))
	}
	app.Exec(t, `DELETE FROM notifications WHERE user_id = $1 AND dedupe_key IN (SELECT 'match:' || j.id FROM jobs j)`, uid)
	app.Notifications.Sweep(ctx)
	app.Notifications.Sweep(ctx)
	if n := app.QueryInt(t, `SELECT count(*) FROM notifications WHERE user_id = $1 AND dedupe_key LIKE 'match-digest:%'`, uid); n != 1 {
		t.Fatalf("digests = %d", n)
	}
	// Read state and isolation.
	c.Do(http.MethodGet, "/api/v1/notifications", nil).JSON(t, &l)
	if r := c.Do(http.MethodPost, "/api/v1/notifications/"+l.Data[0].ID+"/read", nil); r.Code != 204 {
		t.Fatalf("read: %d", r.Code)
	}
	other := app.Client(t)
	other.Register("notif-other@example.com")
	if r := other.Do(http.MethodPost, "/api/v1/notifications/"+l.Data[1].ID+"/read", nil); r.Code != 404 {
		t.Fatalf("cross-user read: %d", r.Code)
	}
	var ol notifList
	other.Do(http.MethodGet, "/api/v1/notifications", nil).JSON(t, &ol)
	if len(ol.Data) != 0 {
		t.Fatal("cross-user notifications visible")
	}
	c.Do(http.MethodPost, "/api/v1/notifications/read-all", nil)
	c.Do(http.MethodGet, "/api/v1/notifications?unread=true", nil).JSON(t, &l)
	if l.Unread != 0 || len(l.Data) != 0 {
		t.Fatalf("after read-all: %+v", l)
	}
	// Turning a kind off stops new notifications of that kind.
	c.Do(http.MethodPut, "/api/v1/notifications/preferences", map[string]any{"high_match": false, "min_match_score": 80, "approvals": false,
		"workflow_events": false, "replies": true, "follow_ups": true, "weekly_report": true})
	app.Exec(t, `DELETE FROM notifications WHERE user_id = $1`, uid)
	app.Notifications.Sweep(ctx)
	if n := app.QueryInt(t, `SELECT count(*) FROM notifications WHERE user_id = $1`, uid); n != 0 {
		t.Fatalf("notifications despite preferences: %d", n)
	}
}

type overviewOut struct {
	Funnel struct {
		Matched int `json:"jobs_matched"`
		Stages  []struct {
			Key          string
			Count        int
			FromPrevious *struct {
				Value     *float64
				LowSample bool `json:"low_sample"`
			} `json:"from_previous"`
		}
	}
	Durations []struct {
		Key        string
		MedianDays *float64 `json:"median_days"`
		Samples    int
	}
	Sources []struct {
		Source                  string
		Applications, Submitted int
		Responses, Interviews   int
		ResponseRate            struct {
			LowSample bool `json:"low_sample"`
		} `json:"response_rate"`
	}
	Weekly []struct{ Applications, Submitted, Responses int }
}

func TestAnalyticsDeriveFromRecords(t *testing.T) {
	app, c, uid, jobs := setupApps(t)
	ctx := context.Background()

	// Application 1: all the way to an interview, with evidence. Application 2: only shortlisted.
	var a1, a2 appDetail
	c.Do(http.MethodPost, "/api/v1/applications", map[string]any{"job_id": jobs[0]}).JSON(t, &a1)
	c.Do(http.MethodPost, "/api/v1/applications", map[string]any{"job_id": jobs[1]}).JSON(t, &a2)
	p := "/api/v1/applications/" + a1.ID
	c.Do(http.MethodPost, p+"/transitions", map[string]any{"to": "PREPARING"})
	approvedCV(t, c, jobs[0])
	c.Do(http.MethodPost, p+"/transitions", map[string]any{"to": "AWAITING_APPROVAL"})
	c.Do(http.MethodPost, p+"/approval", map[string]any{"channel": "ASSISTED", "confirm": true})
	var att struct{ Attempt struct{ ID string } }
	doKey(c, p+"/submissions", "analytics-key-1").JSON(t, &att)
	c.Do(http.MethodPost, p+"/submissions/"+att.Attempt.ID+"/confirm", map[string]any{"attest": true})
	c.Do(http.MethodPost, p+"/transitions", map[string]any{"to": "INTERVIEWING"})

	var o overviewOut
	r := c.Do(http.MethodGet, "/api/v1/analytics/overview?period=30", nil)
	if r.Code != 200 {
		t.Fatalf("overview: %d %s", r.Code, r.Body)
	}
	r.JSON(t, &o)
	want := map[string]int{"SHORTLISTED": 2, "PREPARING": 1, "APPROVED": 1, "SUBMITTED": 1, "RESPONDED": 1, "INTERVIEW": 1, "OFFER": 0}
	for _, s := range o.Funnel.Stages {
		if s.Count != want[s.Key] {
			t.Errorf("stage %s = %d, want %d", s.Key, s.Count, want[s.Key])
		}
	}
	prep := o.Funnel.Stages[1].FromPrevious
	if prep == nil || prep.Value == nil || *prep.Value != 0.5 || !prep.LowSample {
		t.Fatalf("conversion SHORTLISTED→PREPARING: %+v", prep)
	}
	for _, d := range o.Durations {
		if (d.Key == "SHORTLIST_TO_SUBMIT" || d.Key == "SUBMIT_TO_RESPONSE" || d.Key == "SUBMIT_TO_INTERVIEW") && (d.Samples != 1 || d.MedianDays == nil) {
			t.Errorf("duration %s: %+v", d.Key, d)
		}
		if d.Key == "SUBMIT_TO_OFFER" && (d.Samples != 0 || d.MedianDays != nil) {
			t.Errorf("offer duration should be unknown: %+v", d)
		}
	}
	if len(o.Sources) == 0 || o.Sources[0].Applications != 2 || o.Sources[0].Submitted != 1 || o.Sources[0].Interviews != 1 || !o.Sources[0].ResponseRate.LowSample {
		t.Fatalf("sources: %+v", o.Sources)
	}
	apps, sub := 0, 0
	for _, w := range o.Weekly {
		apps += w.Applications
		sub += w.Submitted
	}
	if apps != 2 || sub != 1 {
		t.Fatalf("weekly: apps %d submitted %d", apps, sub)
	}
	if r := c.Do(http.MethodGet, "/api/v1/analytics/overview?period=7", nil); r.Code != 422 {
		t.Fatalf("bad period: %d", r.Code)
	}

	// Reports: on demand, and weekly (once per period).
	var rep struct {
		ID   string
		Data struct {
			Submitted, Interviews, Shortlisted int
			Highlights, Recommendations        []string
		}
	}
	r = c.Do(http.MethodPost, "/api/v1/analytics/reports", nil)
	if r.Code != 201 {
		t.Fatalf("report: %d %s", r.Code, r.Body)
	}
	r.JSON(t, &rep)
	if rep.Data.Submitted != 1 || rep.Data.Interviews != 1 || rep.Data.Shortlisted != 2 || len(rep.Data.Highlights) == 0 || len(rep.Data.Recommendations) == 0 {
		t.Fatalf("report data: %+v", rep.Data)
	}
	n1, err := app.Analytics.GenerateWeekly(ctx)
	if err != nil || n1 != 1 {
		t.Fatalf("weekly: %d %v", n1, err)
	}
	if n2, _ := app.Analytics.GenerateWeekly(ctx); n2 != 0 {
		t.Fatalf("weekly report duplicated: %d", n2)
	}
	app.Notifications.Sweep(ctx)
	if n := app.QueryInt(t, `SELECT count(*) FROM notifications WHERE user_id = $1 AND kind = 'REPORT_READY'`, uid); n != 1 {
		t.Fatalf("report notifications = %d", n)
	}
	// Isolation: another user's analytics are empty.
	other := app.Client(t)
	other.Register("analytics-other@example.com")
	var oo overviewOut
	other.Do(http.MethodGet, "/api/v1/analytics/overview?period=0", nil).JSON(t, &oo)
	if oo.Funnel.Stages[0].Count != 0 || len(oo.Sources) != 0 {
		t.Fatalf("cross-user analytics: %+v", oo.Funnel)
	}
	if r := other.Do(http.MethodGet, "/api/v1/analytics/reports/"+rep.ID, nil); r.Code != 404 {
		t.Fatalf("cross-user report: %d", r.Code)
	}
}
