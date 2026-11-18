// Package notifications delivers in-app notifications (ADR-025). They are
// derived from persisted records by a periodic sweep, so producers stay
// decoupled, and each has a dedupe key that is unique per user: the same
// event never notifies twice, however often the sweep runs.
package notifications

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/careerpilot/careerpilot/services/api/internal/auth"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
)

const (
	// Only records this recent are considered, so enabling a preference or a
	// first sweep never floods the user with old events.
	window = 48 * time.Hour
	// At most this many individual high-match notifications per user per day;
	// beyond that a single daily digest is kept up to date.
	maxMatchPerDay = 5
)

type Notification struct {
	ID        uuid.UUID  `json:"id"`
	Kind      string     `json:"kind"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	Link      string     `json:"link"`
	ReadAt    *time.Time `json:"read_at"`
	CreatedAt time.Time  `json:"created_at"`
}

type New struct {
	Kind, DedupeKey, Title, Body, Link string
}

// Notify inserts a notification unless one with the same key exists.
func Notify(ctx context.Context, q db.Querier, userID uuid.UUID, n New) (bool, error) {
	tag, err := q.Exec(ctx, `INSERT INTO notifications (id, user_id, kind, dedupe_key, title, body, link) VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (user_id, dedupe_key) DO NOTHING`, ids.New(), userID, n.Kind, n.DedupeKey, trunc(n.Title, 200), trunc(n.Body, 1000), n.Link)
	return tag.RowsAffected() == 1, err
}

func trunc(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

type Preferences struct {
	HighMatch      bool `json:"high_match"`
	MinMatchScore  int  `json:"min_match_score"`
	Approvals      bool `json:"approvals"`
	WorkflowEvents bool `json:"workflow_events"`
	Replies        bool `json:"replies"`
	FollowUps      bool `json:"follow_ups"`
	WeeklyReport   bool `json:"weekly_report"`
}

func defaults() Preferences {
	return Preferences{HighMatch: true, MinMatchScore: 80, Approvals: true, WorkflowEvents: true, Replies: true, FollowUps: true, WeeklyReport: true}
}

// LoadPreferences returns stored preferences, or the defaults.
func LoadPreferences(ctx context.Context, q db.Querier, userID uuid.UUID) (Preferences, error) {
	p := defaults()
	err := q.QueryRow(ctx, `SELECT high_match, min_match_score, approvals, workflow_events, replies, follow_ups, weekly_report
		FROM notification_preferences WHERE user_id = $1`, userID).Scan(&p.HighMatch, &p.MinMatchScore, &p.Approvals, &p.WorkflowEvents,
		&p.Replies, &p.FollowUps, &p.WeeklyReport)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil
	}
	return p, err
}

type Service struct {
	pool *pgxpool.Pool
	log  *slog.Logger
	now  func() time.Time
}

func NewService(pool *pgxpool.Pool, log *slog.Logger) *Service {
	return &Service{pool: pool, log: log, now: time.Now}
}

// Sweep derives notifications from recent records for every user. Each
// source query looks back over the window; dedupe keys make it idempotent.
func (s *Service) Sweep(ctx context.Context) (int, error) {
	since := s.now().Add(-window)
	created := 0
	add := func(userID uuid.UUID, n New) error {
		ok, err := Notify(ctx, s.pool, userID, n)
		if ok {
			created++
		}
		return err
	}
	prefs := map[uuid.UUID]Preferences{}
	pref := func(u uuid.UUID) (Preferences, error) {
		if p, ok := prefs[u]; ok {
			return p, nil
		}
		p, err := LoadPreferences(ctx, s.pool, u)
		prefs[u] = p
		return p, err
	}

	type row struct {
		user          uuid.UUID
		id, key, link string
		a, b          string
		when          time.Time
	}
	run := func(sql string, build func(r row, p Preferences) (New, bool)) error {
		rows, err := s.pool.Query(ctx, sql, since)
		if err != nil {
			return err
		}
		items, err := pgx.CollectRows(rows, func(cr pgx.CollectableRow) (row, error) {
			var r row
			return r, cr.Scan(&r.user, &r.id, &r.link, &r.a, &r.b, &r.when)
		})
		if err != nil {
			return err
		}
		for _, r := range items {
			p, err := pref(r.user)
			if err != nil {
				return err
			}
			if n, ok := build(r, p); ok {
				if err := add(r.user, n); err != nil {
					return err
				}
			}
		}
		return nil
	}

	// Approval requests waiting for the user.
	if err := run(`SELECT user_id, id::text, workflow_id::text, summary, '', created_at FROM approval_requests
		WHERE status = 'PENDING' AND created_at > $1`, func(r row, p Preferences) (New, bool) {
		return New{Kind: "APPROVAL_REQUESTED", DedupeKey: "approval:" + r.id, Title: "Approval needed", Body: r.a,
			Link: "/intelligence/workflows/" + r.link}, p.Approvals
	}); err != nil {
		return created, err
	}
	// Workflows waiting for input, and finished workflows.
	if err := run(`SELECT user_id, id::text, id::text, status, coalesce(status_reason, ''), updated_at FROM workflow_executions
		WHERE status = 'NEEDS_INPUT' AND updated_at > $1`, func(r row, p Preferences) (New, bool) {
		return New{Kind: "WORKFLOW_NEEDS_INPUT", DedupeKey: fmt.Sprintf("wf-input:%s:%d", r.id, r.when.Unix()), Title: "A workflow needs your input",
			Body: r.b, Link: "/intelligence/workflows/" + r.link}, p.WorkflowEvents
	}); err != nil {
		return created, err
	}
	if err := run(`SELECT user_id, id::text, id::text, status, coalesce(report->>'headline', ''), completed_at FROM workflow_executions
		WHERE status IN ('SUCCEEDED','PARTIALLY_SUCCEEDED','FAILED') AND completed_at > $1`, func(r row, p Preferences) (New, bool) {
		title := map[string]string{"SUCCEEDED": "Workflow completed", "PARTIALLY_SUCCEEDED": "Workflow partly completed", "FAILED": "Workflow failed"}[r.a]
		return New{Kind: "WORKFLOW_FINISHED", DedupeKey: fmt.Sprintf("wf-done:%s:%d", r.id, r.when.Unix()), Title: title, Body: r.b,
			Link: "/intelligence/workflows/" + r.link}, p.WorkflowEvents
	}); err != nil {
		return created, err
	}
	// Replies and bounces.
	if err := run(`SELECT r.user_id, r.id::text, m.id::text, CASE WHEN r.is_bounce THEN 'B' ELSE 'R' END, coalesce(c.name, m.to_email::text), r.created_at
		FROM outreach_replies r JOIN outreach_messages m ON m.id = r.message_id JOIN recruiter_contacts c ON c.id = m.contact_id
		WHERE r.created_at > $1`, func(r row, p Preferences) (New, bool) {
		if r.a == "B" {
			return New{Kind: "MESSAGE_BOUNCED", DedupeKey: "reply:" + r.id, Title: "An email bounced", Body: "Your email to " + r.b + " was not delivered.",
				Link: "/outreach/messages/" + r.link}, p.Replies
		}
		return New{Kind: "REPLY_RECEIVED", DedupeKey: "reply:" + r.id, Title: "Reply received", Body: r.b + " replied to your email.",
			Link: "/outreach/messages/" + r.link}, p.Replies
	}); err != nil {
		return created, err
	}
	// Follow-up drafts ready for review.
	if err := run(`SELECT f.user_id, f.id::text, f.draft_id::text, '', '', f.updated_at FROM outreach_follow_ups f
		WHERE f.status = 'DRAFTED' AND f.draft_id IS NOT NULL AND f.updated_at > $1`, func(r row, p Preferences) (New, bool) {
		return New{Kind: "FOLLOW_UP_READY", DedupeKey: "followup:" + r.id, Title: "Follow-up ready to review",
			Body: "No reply yet. A follow-up draft is waiting for your approval.", Link: "/outreach/messages/" + r.link}, p.FollowUps
	}); err != nil {
		return created, err
	}
	// Scheduled career reports.
	if err := run(`SELECT user_id, id::text, id::text, to_char(period_start, 'DD Mon'), to_char(period_end, 'DD Mon YYYY'), created_at
		FROM career_reports WHERE trigger = 'SCHEDULED' AND created_at > $1`, func(r row, p Preferences) (New, bool) {
		return New{Kind: "REPORT_READY", DedupeKey: "report:" + r.id, Title: "Your weekly career report", Body: r.a + " – " + r.b,
			Link: "/analytics?report=" + r.link}, p.WeeklyReport
	}); err != nil {
		return created, err
	}
	n, err := s.sweepMatches(ctx, since, pref)
	return created + n, err
}

// sweepMatches notifies about new, eligible-or-unknown, high-scoring jobs.
// Only jobs first seen recently count (a recompute of an old job is not news).
func (s *Service) sweepMatches(ctx context.Context, since time.Time, pref func(uuid.UUID) (Preferences, error)) (int, error) {
	rows, err := s.pool.Query(ctx, `SELECT r.user_id, j.id, j.title, j.company_name, r.overall_score::float8
		FROM job_match_results r JOIN jobs j ON j.id = r.job_id
		WHERE r.computed_at > $1 AND j.first_seen_at > $1 AND j.expired_at IS NULL AND j.duplicate_of IS NULL
		  AND r.eligibility <> 'INELIGIBLE' AND r.overall_score >= 50
		ORDER BY r.user_id, r.overall_score DESC`, since)
	if err != nil {
		return 0, err
	}
	type m struct {
		user           uuid.UUID
		job            uuid.UUID
		title, company string
		score          float64
	}
	items, err := pgx.CollectRows(rows, func(cr pgx.CollectableRow) (m, error) {
		var x m
		return x, cr.Scan(&x.user, &x.job, &x.title, &x.company, &x.score)
	})
	if err != nil {
		return 0, err
	}
	created := 0
	day := s.now().UTC().Format("2006-01-02")
	overflow := map[uuid.UUID]int{}
	for _, x := range items {
		p, err := pref(x.user)
		if err != nil {
			return created, err
		}
		if !p.HighMatch || x.score < float64(p.MinMatchScore) {
			continue
		}
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM notifications WHERE user_id = $1 AND dedupe_key = $2)`,
			x.user, "match:"+x.job.String()).Scan(&exists); err != nil {
			return created, err
		}
		if exists {
			continue
		}
		var today int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id = $1 AND kind = 'HIGH_MATCH'
			AND dedupe_key LIKE 'match:%' AND created_at >= date_trunc('day', now() AT TIME ZONE 'UTC')`, x.user).Scan(&today); err != nil {
			return created, err
		}
		if today >= maxMatchPerDay {
			overflow[x.user]++
			continue
		}
		ok, err := Notify(ctx, s.pool, x.user, New{Kind: "HIGH_MATCH", DedupeKey: "match:" + x.job.String(),
			Title: fmt.Sprintf("New strong match: %.0f", x.score), Body: x.title + " at " + x.company, Link: "/jobs/" + x.job.String()})
		if err != nil {
			return created, err
		}
		if ok {
			created++
		}
	}
	for u, n := range overflow {
		p, err := pref(u)
		if err != nil {
			return created, err
		}
		// One digest per day, updated rather than duplicated.
		if _, err := s.pool.Exec(ctx, `INSERT INTO notifications (id, user_id, kind, dedupe_key, title, body, link)
			VALUES ($1, $2, 'HIGH_MATCH', $3, 'More strong matches today', $4, '/discover?sort=match')
			ON CONFLICT (user_id, dedupe_key) DO UPDATE SET body = EXCLUDED.body, read_at = NULL`,
			ids.New(), u, "match-digest:"+day, fmt.Sprintf("%d more new jobs scored %d or higher. See them on Discover.", n, p.MinMatchScore)); err != nil {
			return created, err
		}
	}
	return created, nil
}

// ---- Background job ----

type SweepArgs struct{}

func (SweepArgs) Kind() string { return "notifications_sweep" }
func (SweepArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 1, UniqueOpts: river.UniqueOpts{ByPeriod: time.Minute}}
}

type SweepWorker struct {
	river.WorkerDefaults[SweepArgs]
	Service *Service
}

func (w *SweepWorker) Work(ctx context.Context, _ *river.Job[SweepArgs]) error {
	n, err := w.Service.Sweep(ctx)
	if n > 0 {
		w.Service.log.Info("notifications created", "count", n)
	}
	return err
}

func PeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{river.NewPeriodicJob(river.PeriodicInterval(2*time.Minute),
		func() (river.JobArgs, *river.InsertOpts) { return SweepArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true})}
}

// ---- HTTP ----

type HTTP struct{ svc *Service }

func NewHTTP(svc *Service) *HTTP { return &HTTP{svc: svc} }

func (h *HTTP) Routes(r chi.Router) {
	r.Get("/notifications", h.list)
	r.Post("/notifications/read-all", h.readAll)
	r.Post("/notifications/{id}/read", h.read)
	r.Get("/notifications/preferences", h.getPrefs)
	r.Put("/notifications/preferences", h.putPrefs)
}

func uid(r *http.Request) uuid.UUID { return auth.MustPrincipal(r.Context()).UserID }

func (h *HTTP) list(w http.ResponseWriter, r *http.Request) {
	limit := 30
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 100 {
			httpx.WriteError(w, r, httpx.Validation(map[string]string{"limit": "Between 1 and 100."}))
			return
		}
		limit = n
	}
	unreadOnly := r.URL.Query().Get("unread") == "true"
	rows, err := h.svc.pool.Query(r.Context(), `SELECT id, kind, title, body, link, read_at, created_at FROM notifications
		WHERE user_id = $1 AND (NOT $2 OR read_at IS NULL) ORDER BY created_at DESC LIMIT $3`, uid(r), unreadOnly, limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items, err := pgx.CollectRows(rows, func(cr pgx.CollectableRow) (Notification, error) {
		var n Notification
		return n, cr.Scan(&n.ID, &n.Kind, &n.Title, &n.Body, &n.Link, &n.ReadAt, &n.CreatedAt)
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if items == nil {
		items = []Notification{}
	}
	var unread int
	if err := h.svc.pool.QueryRow(r.Context(), `SELECT count(*) FROM notifications WHERE user_id = $1 AND read_at IS NULL`, uid(r)).Scan(&unread); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": items, "unread_count": unread})
}

func (h *HTTP) read(w http.ResponseWriter, r *http.Request) {
	id, ok := ids.Parse(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeNotFound, "Notification not found."))
		return
	}
	tag, err := h.svc.pool.Exec(r.Context(), `UPDATE notifications SET read_at = coalesce(read_at, now()) WHERE id = $1 AND user_id = $2`, id, uid(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeNotFound, "Notification not found."))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTP) readAll(w http.ResponseWriter, r *http.Request) {
	if _, err := h.svc.pool.Exec(r.Context(), `UPDATE notifications SET read_at = now() WHERE user_id = $1 AND read_at IS NULL`, uid(r)); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTP) getPrefs(w http.ResponseWriter, r *http.Request) {
	p, err := LoadPreferences(r.Context(), h.svc.pool, uid(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *HTTP) putPrefs(w http.ResponseWriter, r *http.Request) {
	var p Preferences
	if err := httpx.DecodeJSON(r, &p); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if p.MinMatchScore < 50 || p.MinMatchScore > 100 {
		httpx.WriteError(w, r, httpx.Validation(map[string]string{"min_match_score": "Between 50 and 100."}))
		return
	}
	if _, err := h.svc.pool.Exec(r.Context(), `INSERT INTO notification_preferences (user_id, high_match, min_match_score, approvals, workflow_events,
		replies, follow_ups, weekly_report) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (user_id) DO UPDATE SET high_match=EXCLUDED.high_match,
		min_match_score=EXCLUDED.min_match_score, approvals=EXCLUDED.approvals, workflow_events=EXCLUDED.workflow_events, replies=EXCLUDED.replies,
		follow_ups=EXCLUDED.follow_ups, weekly_report=EXCLUDED.weekly_report, updated_at=now()`, uid(r), p.HighMatch, p.MinMatchScore, p.Approvals,
		p.WorkflowEvents, p.Replies, p.FollowUps, p.WeeklyReport); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}
