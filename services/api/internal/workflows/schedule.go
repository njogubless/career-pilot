package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/careerpilot/careerpilot/services/api/internal/audit"
	"github.com/careerpilot/careerpilot/services/api/internal/automation"
	"github.com/careerpilot/careerpilot/services/api/internal/notifications"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
)

const maxSchedulesPerUser = 5

// Schedule is a recurring objective (master prompt §12): the same validated
// plan, started at a fixed local time every day or every week.
type Schedule struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	Objective   string     `json:"objective"`
	Template    string     `json:"template"`
	Params      Params     `json:"params"`
	Cadence     string     `json:"cadence"` // DAILY | WEEKLY
	Weekday     *int       `json:"weekday"` // 0 = Sunday
	Hour        int        `json:"hour"`
	Timezone    string     `json:"timezone"`
	Enabled     bool       `json:"enabled"`
	NextRunAt   time.Time  `json:"next_run_at"`
	LastRunAt   *time.Time `json:"last_run_at"`
	LastOutcome *string    `json:"last_outcome"`
	LastMessage *string    `json:"last_message"`
	CreatedAt   time.Time  `json:"created_at"`
	userID      uuid.UUID
}

// NextRun returns the first run time strictly after `after`, at `hour`:00 in
// the schedule's time zone (DST-safe: built from the local calendar date).
func NextRun(cadence string, weekday *int, hour int, loc *time.Location, after time.Time) time.Time {
	t := after.In(loc)
	cand := time.Date(t.Year(), t.Month(), t.Day(), hour, 0, 0, 0, loc)
	if cadence == "WEEKLY" && weekday != nil {
		cand = cand.AddDate(0, 0, (*weekday-int(cand.Weekday())+7)%7)
		if !cand.After(after) {
			cand = cand.AddDate(0, 0, 7)
		}
		return cand
	}
	if !cand.After(after) {
		cand = cand.AddDate(0, 0, 1)
	}
	return cand
}

type ScheduleInput struct {
	Name      string          `json:"name"`
	Objective string          `json:"objective"`
	Template  string          `json:"template"`
	Params    json.RawMessage `json:"params"`
	Cadence   string          `json:"cadence"`
	Weekday   *int            `json:"weekday"`
	Hour      int             `json:"hour"`
	Timezone  string          `json:"timezone"`
	Enabled   *bool           `json:"enabled"`
}

func (in *ScheduleInput) validate() (Proposal, *time.Location, error) {
	errs := map[string]string{}
	in.Name = strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(in.Name); n < 1 || n > 120 {
		errs["name"] = "Give the schedule a name (up to 120 characters)."
	}
	if utf8.RuneCountInString(in.Objective) > 2000 {
		errs["objective"] = "Use at most 2000 characters."
	}
	switch in.Cadence {
	case "DAILY":
		in.Weekday = nil
	case "WEEKLY":
		if in.Weekday == nil || *in.Weekday < 0 || *in.Weekday > 6 {
			errs["weekday"] = "Choose a day of the week."
		}
	default:
		errs["cadence"] = "Choose DAILY or WEEKLY."
	}
	if in.Hour < 0 || in.Hour > 23 {
		errs["hour"] = "Between 0 and 23."
	}
	loc, err := time.LoadLocation(in.Timezone)
	if err != nil || in.Timezone == "" || in.Timezone == "Local" {
		errs["timezone"] = "Use an IANA time zone such as Africa/Nairobi."
	}
	if len(errs) > 0 {
		return Proposal{}, nil, httpx.Validation(errs)
	}
	prop, err := Propose(in.Template, in.Params)
	return prop, loc, err
}

const schedCols = `id, user_id, name, objective, template, params, cadence, weekday, hour, timezone, enabled, next_run_at, last_run_at,
	last_outcome, last_message, created_at`

func scanSchedule(r pgx.Row) (Schedule, error) {
	var s Schedule
	var params []byte
	err := r.Scan(&s.ID, &s.userID, &s.Name, &s.Objective, &s.Template, &params, &s.Cadence, &s.Weekday, &s.Hour, &s.Timezone, &s.Enabled,
		&s.NextRunAt, &s.LastRunAt, &s.LastOutcome, &s.LastMessage, &s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, httpx.NewError(httpx.CodeNotFound, "Schedule not found.")
	}
	_ = json.Unmarshal(params, &s.Params)
	return s, err
}

func (e *Engine) ListSchedules(ctx context.Context, userID uuid.UUID) ([]Schedule, error) {
	rows, err := e.pool.Query(ctx, `SELECT `+schedCols+` FROM workflow_schedules WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Schedule, error) { return scanSchedule(r) })
	if out == nil {
		out = []Schedule{}
	}
	return out, err
}

// SaveSchedule creates (id == nil) or updates a schedule.
func (e *Engine) SaveSchedule(ctx context.Context, userID uuid.UUID, id *uuid.UUID, in ScheduleInput, ip string) (Schedule, error) {
	prop, loc, err := in.validate()
	if err != nil {
		return Schedule{}, err
	}
	enabled := in.Enabled == nil || *in.Enabled
	params, _ := json.Marshal(prop.Params)
	next := NextRun(in.Cadence, in.Weekday, in.Hour, loc, e.now())
	var sid uuid.UUID
	err = db.InTx(ctx, e.pool, func(tx pgx.Tx) error {
		if id == nil {
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM workflow_schedules WHERE user_id = $1`, userID).Scan(&n); err != nil {
				return err
			}
			if n >= maxSchedulesPerUser {
				return httpx.NewError(httpx.CodeConflict, fmt.Sprintf("You can have at most %d schedules.", maxSchedulesPerUser))
			}
			sid = ids.New()
			if _, err := tx.Exec(ctx, `INSERT INTO workflow_schedules (id, user_id, name, objective, template, params, cadence, weekday, hour,
				timezone, enabled, next_run_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, sid, userID, in.Name, in.Objective,
				prop.Template.Name, params, in.Cadence, in.Weekday, in.Hour, in.Timezone, enabled, next); err != nil {
				return err
			}
		} else {
			sid = *id
			tag, err := tx.Exec(ctx, `UPDATE workflow_schedules SET name=$3, objective=$4, template=$5, params=$6, cadence=$7, weekday=$8, hour=$9,
				timezone=$10, enabled=$11, next_run_at=$12, updated_at=now() WHERE id = $1 AND user_id = $2`, sid, userID, in.Name, in.Objective,
				prop.Template.Name, params, in.Cadence, in.Weekday, in.Hour, in.Timezone, enabled, next)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return httpx.NewError(httpx.CodeNotFound, "Schedule not found.")
			}
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "workflow.schedule.save", SubjectType: "workflow_schedule", SubjectID: &sid,
			Outcome: audit.Success, IP: ip, Metadata: map[string]any{"cadence": in.Cadence, "enabled": enabled, "template": prop.Template.Name}})
	})
	if err != nil {
		return Schedule{}, err
	}
	return scanSchedule(e.pool.QueryRow(ctx, `SELECT `+schedCols+` FROM workflow_schedules WHERE id = $1`, sid))
}

func (e *Engine) DeleteSchedule(ctx context.Context, userID, id uuid.UUID, ip string) error {
	tag, err := e.pool.Exec(ctx, `DELETE FROM workflow_schedules WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httpx.NewError(httpx.CodeNotFound, "Schedule not found.")
	}
	return audit.Record(ctx, e.pool, audit.Event{UserID: &userID, Action: "workflow.schedule.delete", SubjectType: "workflow_schedule",
		SubjectID: &id, Outcome: audit.Success, IP: ip})
}

// RunDueSchedules starts the runs that are due. Each due schedule fires at
// most once per sweep (missed slots during downtime collapse into one run),
// never overlaps its own active run, and respects the global pause. The
// (schedule, slot) unique index makes a double fire harmless.
func (e *Engine) RunDueSchedules(ctx context.Context) (int, error) {
	rows, err := e.pool.Query(ctx, `SELECT `+schedCols+` FROM workflow_schedules WHERE enabled AND next_run_at <= $1 ORDER BY next_run_at LIMIT 100`, e.now())
	if err != nil {
		return 0, err
	}
	due, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Schedule, error) { return scanSchedule(r) })
	if err != nil {
		return 0, err
	}
	started := 0
	for _, s := range due {
		outcome, _, err := e.fire(ctx, s)
		if err != nil {
			return started, err
		}
		if outcome == "STARTED" {
			started++
		}
	}
	return started, nil
}

func (e *Engine) fire(ctx context.Context, s Schedule) (string, string, error) {
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		loc = time.UTC
	}
	slotAt := s.NextRunAt
	outcome, msg := "STARTED", ""
	set, err := automation.Load(ctx, e.pool, s.userID)
	if err != nil {
		return "", "", err
	}
	var overlap bool
	if err := e.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM workflow_executions WHERE schedule_id = $1 AND status = ANY($2))`,
		s.ID, activeStatuses).Scan(&overlap); err != nil {
		return "", "", err
	}
	switch {
	case set.Paused:
		outcome, msg = "SKIPPED_PAUSED", "Automation was paused."
	case overlap:
		outcome, msg = "SKIPPED_OVERLAP", "The previous run of this schedule had not finished."
	default:
		params, _ := json.Marshal(s.Params)
		_, err := e.create(ctx, s.userID, CreateInput{Objective: s.Objective, Template: s.Template, Params: params, Interpretation: "STRUCTURED"},
			&slot{ScheduleID: s.ID, At: slotAt}, "")
		var he *httpx.Error
		switch {
		case errors.Is(err, errSlotTaken):
			// Already started for this slot (e.g. a crash before the schedule advanced).
		case errors.As(err, &he) && he.Code == httpx.CodeConflict:
			outcome, msg = "SKIPPED_LIMIT", he.Message
		case err != nil:
			outcome, msg = "FAILED", "The run could not be started."
			e.log.Warn("scheduled run failed", "schedule_id", s.ID, "err", err)
		}
	}
	next := NextRun(s.Cadence, s.Weekday, s.Hour, loc, maxTime(e.now(), slotAt))
	var msgP *string
	if msg != "" {
		msgP = &msg
	}
	if _, err := e.pool.Exec(ctx, `UPDATE workflow_schedules SET next_run_at = $2, last_run_at = now(), last_outcome = $3, last_message = $4,
		updated_at = now() WHERE id = $1`, s.ID, next, outcome, msgP); err != nil {
		return "", "", err
	}
	// An overlap is expected (e.g. the last run is waiting for approval), so it
	// is recorded on the schedule but not notified.
	if outcome != "STARTED" && outcome != "SKIPPED_OVERLAP" {
		_, _ = notifications.Notify(ctx, e.pool, s.userID, notifications.New{Kind: "SCHEDULE_SKIPPED",
			DedupeKey: fmt.Sprintf("schedule:%s:%d", s.ID, slotAt.Unix()), Title: "Scheduled run skipped: " + s.Name, Body: msg, Link: "/intelligence"})
	}
	_ = audit.Record(ctx, e.pool, audit.Event{UserID: &s.userID, Actor: audit.ActorSystem, Action: "workflow.schedule.fire",
		SubjectType: "workflow_schedule", SubjectID: &s.ID, Outcome: audit.Success, Metadata: map[string]any{"outcome": outcome}})
	return outcome, msg, nil
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

type ScheduleArgs struct{}

func (ScheduleArgs) Kind() string { return "workflow_scheduler" }
func (ScheduleArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 1, UniqueOpts: river.UniqueOpts{ByPeriod: time.Minute}}
}

type ScheduleWorker struct {
	river.WorkerDefaults[ScheduleArgs]
	Engine *Engine
}

func (w *ScheduleWorker) Work(ctx context.Context, _ *river.Job[ScheduleArgs]) error {
	n, err := w.Engine.RunDueSchedules(ctx)
	if n > 0 {
		w.Engine.log.Info("scheduled workflows started", "count", n)
	}
	return err
}

// ---- HTTP ----

func (h *HTTP) scheduleRoutes(r chi.Router) {
	r.Get("/workflow-schedules", h.listSchedules)
	r.Post("/workflow-schedules", h.saveSchedule(false))
	r.Put("/workflow-schedules/{id}", h.saveSchedule(true))
	r.Delete("/workflow-schedules/{id}", h.deleteSchedule)
}

func (h *HTTP) listSchedules(w http.ResponseWriter, r *http.Request) {
	out, err := h.e.ListSchedules(r.Context(), uid(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (h *HTTP) saveSchedule(update bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var id *uuid.UUID
		if update {
			x, ok := idParam(w, r)
			if !ok {
				return
			}
			id = &x
		}
		var in ScheduleInput
		if err := httpx.DecodeJSON(r, &in); err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		s, err := h.e.SaveSchedule(r.Context(), uid(r), id, in, h.authH.ClientIP(r))
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		status := http.StatusOK
		if !update {
			status = http.StatusCreated
		}
		httpx.WriteJSON(w, status, s)
	}
}

func (h *HTTP) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	if err := h.e.DeleteSchedule(r.Context(), uid(r), id, h.authH.ClientIP(r)); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
