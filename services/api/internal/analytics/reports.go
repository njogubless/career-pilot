package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/careerpilot/careerpilot/services/api/internal/auth"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ratelimit"
)

// ReportData is a career progress report for a period. Highlights and
// recommendations are rules over the numbers (no LLM), so every sentence can
// be traced to the counts beside it.
type ReportData struct {
	PeriodStart     time.Time    `json:"period_start"`
	PeriodEnd       time.Time    `json:"period_end"`
	StrongMatches   int          `json:"strong_matches"`
	Shortlisted     int          `json:"shortlisted"`
	Submitted       int          `json:"submitted"`
	Responses       int          `json:"responses"`
	Interviews      int          `json:"interviews"`
	Offers          int          `json:"offers"`
	Rejections      int          `json:"rejections"`
	OutreachSent    int          `json:"outreach_sent"`
	OutreachReplies int          `json:"outreach_replies"`
	TopSkillGaps    []SkillTrend `json:"top_skill_gaps"`
	Highlights      []string     `json:"highlights"`
	Recommendations []string     `json:"recommendations"`
}

type Report struct {
	ID          uuid.UUID  `json:"id"`
	PeriodStart time.Time  `json:"period_start"`
	PeriodEnd   time.Time  `json:"period_end"`
	Trigger     string     `json:"trigger"`
	Data        ReportData `json:"data"`
	CreatedAt   time.Time  `json:"created_at"`
}

// Build computes report data for [start, end).
func (s *Service) Build(ctx context.Context, userID uuid.UUID, start, end time.Time) (ReportData, error) {
	d := ReportData{PeriodStart: start, PeriodEnd: end, Highlights: []string{}, Recommendations: []string{}, TopSkillGaps: []SkillTrend{}}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM job_match_results r JOIN jobs j ON j.id = r.job_id
		WHERE r.user_id = $1 AND r.algorithm_version = $2 AND r.overall_score >= $3 AND r.eligibility <> 'INELIGIBLE'
		AND j.first_seen_at >= $4 AND j.first_seen_at < $5`, userID, s.matchVersion, matchedScore, start, end).Scan(&d.StrongMatches); err != nil {
		return d, err
	}
	// Events in the period, from status history and evidence.
	if err := s.pool.QueryRow(ctx, `SELECT
			count(*) FILTER (WHERE h.from_status IS NULL),
			count(*) FILTER (WHERE h.to_status = 'SUBMITTED'),
			count(*) FILTER (WHERE h.to_status IN ('ACKNOWLEDGED','ASSESSMENT','INTERVIEWING','OFFER','REJECTED')
				AND h.from_status IN ('SUBMITTED')),
			count(*) FILTER (WHERE h.to_status IN ('ASSESSMENT','INTERVIEWING')),
			count(*) FILTER (WHERE h.to_status = 'OFFER'),
			count(*) FILTER (WHERE h.to_status = 'REJECTED')
		FROM application_status_history h WHERE h.user_id = $1 AND h.created_at >= $2 AND h.created_at < $3`, userID, start, end).
		Scan(&d.Shortlisted, &d.Submitted, &d.Responses, &d.Interviews, &d.Offers, &d.Rejections); err != nil {
		return d, err
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE kind = 'INTRODUCTION'),
			count(*) FILTER (WHERE kind = 'INTRODUCTION' AND replied_at IS NOT NULL)
		FROM outreach_messages WHERE user_id = $1 AND status = 'SENT' AND sent_at >= $2 AND sent_at < $3`, userID, start, end).
		Scan(&d.OutreachSent, &d.OutreachReplies); err != nil {
		return d, err
	}
	var o Overview
	if err := s.skillTrends(ctx, userID, end, &o); err != nil {
		return d, err
	}
	if len(o.Skills) > 3 {
		o.Skills = o.Skills[:3]
	}
	d.TopSkillGaps = o.Skills

	// Highlights: plain statements of what happened.
	if d.Offers > 0 {
		d.Highlights = append(d.Highlights, fmt.Sprintf("%d offer(s) recorded.", d.Offers))
	}
	if d.Interviews > 0 {
		d.Highlights = append(d.Highlights, fmt.Sprintf("%d move(s) to an assessment or interview.", d.Interviews))
	}
	d.Highlights = append(d.Highlights, fmt.Sprintf("%d new strong matches; %d shortlisted; %d submitted with evidence.", d.StrongMatches, d.Shortlisted, d.Submitted))
	if d.OutreachSent > 0 {
		d.Highlights = append(d.Highlights, fmt.Sprintf("%d introduction email(s) sent, %d replied.", d.OutreachSent, d.OutreachReplies))
	}

	// Recommendations: only when the data supports them.
	if len(d.TopSkillGaps) > 0 && d.TopSkillGaps[0].Total >= 3 {
		g := d.TopSkillGaps[0]
		d.Recommendations = append(d.Recommendations, fmt.Sprintf("%s is the most common missing requirement across your recent strong matches (%d jobs). "+
			"If you have this skill, add it to your profile; if not, it is a candidate to learn.", g.Skill, g.Total))
	}
	if d.StrongMatches >= 5 && d.Shortlisted == 0 {
		d.Recommendations = append(d.Recommendations, fmt.Sprintf("%d strong matches arrived but none were shortlisted. Review them on Discover.", d.StrongMatches))
	}
	var submittedTotal, respondedTotal int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE submitted_at IS NOT NULL),
			count(*) FILTER (WHERE submitted_at IS NOT NULL AND status IN ('ACKNOWLEDGED','ASSESSMENT','INTERVIEWING','OFFER','REJECTED'))
		FROM applications WHERE user_id = $1 AND submitted_at < $2`, userID, end).Scan(&submittedTotal, &respondedTotal); err != nil {
		return d, err
	}
	if submittedTotal >= MinSample && respondedTotal == 0 {
		d.Recommendations = append(d.Recommendations, fmt.Sprintf("None of your %d submitted applications has a recorded response yet. "+
			"Check that responses are recorded, and consider tailoring CVs to the skill gaps above.", submittedTotal))
	}
	if len(d.Recommendations) == 0 {
		d.Recommendations = append(d.Recommendations, "No recommendation: there is not yet enough activity to suggest changes.")
	}
	return d, nil
}

// store saves a report; lastDay is the last calendar day the period covers.
func (s *Service) store(ctx context.Context, userID uuid.UUID, trigger string, d ReportData, lastDay time.Time) (uuid.UUID, bool, error) {
	raw, _ := json.Marshal(d)
	id := ids.New()
	tag, err := s.pool.Exec(ctx, `INSERT INTO career_reports (id, user_id, period_start, period_end, trigger, data) VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT DO NOTHING`, id, userID, d.PeriodStart, lastDay, trigger, raw)
	return id, tag.RowsAffected() == 1, err
}

// GenerateManual builds a report for the last 7 days on demand.
func (s *Service) GenerateManual(ctx context.Context, userID uuid.UUID) (Report, error) {
	end := s.now().UTC()
	start := end.AddDate(0, 0, -7)
	d, err := s.Build(ctx, userID, start, end)
	if err != nil {
		return Report{}, err
	}
	id, _, err := s.store(ctx, userID, "MANUAL", d, end)
	if err != nil {
		return Report{}, err
	}
	return s.Get(ctx, userID, id)
}

// GenerateWeekly creates last week's report (Monday–Sunday, UTC) for users
// who want it and had any activity in the last four weeks. The unique index
// on scheduled reports makes repeated runs harmless.
func (s *Service) GenerateWeekly(ctx context.Context) (int, error) {
	end := weekStart(s.now())
	start := end.AddDate(0, 0, -7)
	rows, err := s.pool.Query(ctx, `SELECT u.id FROM users u
		LEFT JOIN notification_preferences p ON p.user_id = u.id
		WHERE u.status = 'ACTIVE' AND coalesce(p.weekly_report, true)
		  AND NOT EXISTS (SELECT 1 FROM career_reports c WHERE c.user_id = u.id AND c.trigger = 'SCHEDULED' AND c.period_start = $1)
		  AND (EXISTS (SELECT 1 FROM applications a WHERE a.user_id = u.id AND a.updated_at >= $2)
		    OR EXISTS (SELECT 1 FROM outreach_messages m WHERE m.user_id = u.id AND m.updated_at >= $2)
		    OR EXISTS (SELECT 1 FROM workflow_executions w WHERE w.user_id = u.id AND w.created_at >= $2))`, start, end.AddDate(0, 0, -28))
	if err != nil {
		return 0, err
	}
	users, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, err
	}
	made := 0
	for _, u := range users {
		d, err := s.Build(ctx, u, start, end)
		if err != nil {
			return made, err
		}
		if _, created, err := s.store(ctx, u, "SCHEDULED", d, end.AddDate(0, 0, -1)); err != nil {
			return made, err
		} else if created {
			made++
		}
	}
	return made, nil
}

const reportCols = `id, period_start, period_end, trigger, data, created_at`

func scanReport(r pgx.Row) (Report, error) {
	var rep Report
	var raw []byte
	err := r.Scan(&rep.ID, &rep.PeriodStart, &rep.PeriodEnd, &rep.Trigger, &raw, &rep.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return rep, httpx.NewError(httpx.CodeNotFound, "Report not found.")
	}
	if err == nil {
		err = json.Unmarshal(raw, &rep.Data)
	}
	return rep, err
}

func (s *Service) Get(ctx context.Context, userID, id uuid.UUID) (Report, error) {
	return scanReport(s.pool.QueryRow(ctx, `SELECT `+reportCols+` FROM career_reports WHERE id = $1 AND user_id = $2`, id, userID))
}

func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]Report, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+reportCols+` FROM career_reports WHERE user_id = $1 ORDER BY created_at DESC LIMIT 52`, userID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Report, error) { return scanReport(r) })
	if out == nil {
		out = []Report{}
	}
	return out, err
}

// ---- Background job ----

type WeeklyArgs struct{}

func (WeeklyArgs) Kind() string { return "career_reports_weekly" }
func (WeeklyArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByPeriod: 30 * time.Minute}}
}

type WeeklyWorker struct {
	river.WorkerDefaults[WeeklyArgs]
	Service *Service
	Log     *slog.Logger
}

func (w *WeeklyWorker) Work(ctx context.Context, _ *river.Job[WeeklyArgs]) error {
	n, err := w.Service.GenerateWeekly(ctx)
	if n > 0 && w.Log != nil {
		w.Log.Info("weekly career reports generated", "count", n)
	}
	return err
}

// PeriodicJobs checks hourly; reports are created once per user per week.
func PeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return WeeklyArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true})}
}

// ---- HTTP ----

type HTTP struct {
	svc     *Service
	limiter ratelimit.Limiter
}

func NewHTTP(svc *Service, limiter ratelimit.Limiter) *HTTP { return &HTTP{svc: svc, limiter: limiter} }

func (h *HTTP) Routes(r chi.Router) {
	r.Get("/analytics/overview", h.overview)
	r.Get("/analytics/reports", h.reports)
	r.Post("/analytics/reports", h.generate)
	r.Get("/analytics/reports/{id}", h.report)
}

func uid(r *http.Request) uuid.UUID { return auth.MustPrincipal(r.Context()).UserID }

func (h *HTTP) overview(w http.ResponseWriter, r *http.Request) {
	period := 90
	if s := r.URL.Query().Get("period"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || (n != 0 && n != 30 && n != 90 && n != 365) {
			httpx.WriteError(w, r, httpx.Validation(map[string]string{"period": "Use 30, 90, 365 or 0 (all time)."}))
			return
		}
		period = n
	}
	o, err := h.svc.Overview(r.Context(), uid(r), period)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, o)
}

func (h *HTTP) reports(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.List(r.Context(), uid(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (h *HTTP) generate(w http.ResponseWriter, r *http.Request) {
	if ok, _, err := h.limiter.Allow(r.Context(), "analytics:report:"+uid(r).String(), 10, time.Hour); err == nil && !ok {
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeRateLimited, "Reports were generated recently. Please wait."))
		return
	}
	rep, err := h.svc.GenerateManual(r.Context(), uid(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, rep)
}

func (h *HTTP) report(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeNotFound, "Report not found."))
		return
	}
	rep, err := h.svc.Get(r.Context(), uid(r), id)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rep)
}
