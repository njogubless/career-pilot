// Package automation holds the user's automation controls (mode, global
// pause, daily limits) and the guard that every external or autonomous
// action must pass immediately before it runs (master prompt §10, ADR-022).
package automation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/careerpilot/careerpilot/services/api/internal/audit"
	"github.com/careerpilot/careerpilot/services/api/internal/auth"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
)

// Modes (master prompt §10): A discovery only, B human-approved (default),
// C controlled autonomy within explicit permissions.
const (
	ModeDiscoveryOnly = "DISCOVERY_ONLY"
	ModeHumanApproved = "HUMAN_APPROVED"
	ModeControlled    = "CONTROLLED"

	DefaultDailyLimit = 20
)

// Actions the guard knows about.
const (
	ActionPrepare = "application.prepare"
	ActionSubmit  = "application.submit"
)

type Settings struct {
	Mode                 string     `json:"mode"`
	Paused               bool       `json:"paused"`
	PausedAt             *time.Time `json:"paused_at"`
	DailySubmissionLimit int        `json:"daily_submission_limit"`
	UpdatedAt            *time.Time `json:"updated_at"`
}

func defaults() Settings {
	return Settings{Mode: ModeHumanApproved, DailySubmissionLimit: DefaultDailyLimit}
}

// Load returns the user's settings, or the defaults when none are stored.
func Load(ctx context.Context, q db.Querier, userID uuid.UUID) (Settings, error) {
	s := defaults()
	var updated time.Time
	err := q.QueryRow(ctx, `SELECT mode, paused, paused_at, daily_submission_limit, updated_at FROM automation_settings WHERE user_id = $1`,
		userID).Scan(&s.Mode, &s.Paused, &s.PausedAt, &s.DailySubmissionLimit, &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, nil
	}
	if err != nil {
		return Settings{}, err
	}
	s.UpdatedAt = &updated
	return s, nil
}

// Denied is returned when the guard refuses an action. The message is safe
// to show to the user.
type Denied struct {
	Reason  string // PAUSED | MODE | LIMIT | CHANNEL
	Message string
}

func (d *Denied) Error() string { return "automation denied: " + d.Reason + ": " + d.Message }

// Guard authorises actions. Refusals are audited through its own pool, since
// the caller's transaction is about to roll back.
type Guard struct{ pool *pgxpool.Pool }

func NewGuard(pool *pgxpool.Pool) *Guard { return &Guard{pool: pool} }

// Authorize checks an action immediately before it runs and records refusals.
//
//   - Users acting themselves are never blocked by pause or mode (they are not
//     automation), but the daily submission limit applies to everyone.
//   - Agents and the system are blocked while paused, in DISCOVERY_ONLY mode,
//     and from submitting unless the mode is CONTROLLED.
//
// Callers pass a transaction so the check and the action commit together.
func (g *Guard) Authorize(ctx context.Context, q db.Querier, userID uuid.UUID, actor audit.Actor, action string) error {
	s, err := Load(ctx, q, userID)
	if err != nil {
		return err
	}
	var denied *Denied
	switch {
	case actor != audit.ActorUser && s.Paused:
		denied = &Denied{"PAUSED", "Automation is paused. Resume it in Settings to let CareerPilot act for you."}
	case actor != audit.ActorUser && s.Mode == ModeDiscoveryOnly && (action == ActionPrepare || action == ActionSubmit):
		denied = &Denied{"MODE", "Automation is set to discovery only."}
	case actor != audit.ActorUser && action == ActionSubmit && s.Mode != ModeControlled:
		denied = &Denied{"MODE", "Submissions need your explicit action in human-approved mode."}
	}
	if denied == nil && action == ActionSubmit {
		var n int
		if err := q.QueryRow(ctx, `SELECT count(*) FROM submission_attempts WHERE user_id = $1 AND status <> 'CANCELLED'
			AND started_at > now() - interval '24 hours'`, userID).Scan(&n); err != nil {
			return err
		}
		if n >= s.DailySubmissionLimit {
			denied = &Denied{"LIMIT", fmt.Sprintf("Your limit of %d submissions per 24 hours has been reached. You can change it in Settings.", s.DailySubmissionLimit)}
		}
	}
	if denied != nil {
		_ = audit.Record(context.WithoutCancel(ctx), g.pool, audit.Event{UserID: &userID, Actor: actor, Action: action,
			SubjectType: "automation", Outcome: audit.Denied, Metadata: map[string]any{"reason": denied.Reason, "mode": s.Mode, "paused": s.Paused}})
		return denied
	}
	return nil
}

// AsHTTP maps a Denied error onto the API error envelope.
func AsHTTP(err error) error {
	var d *Denied
	if errors.As(err, &d) {
		code := httpx.CodeForbidden
		if d.Reason == "LIMIT" {
			code = httpx.CodeRateLimited
		}
		return &httpx.Error{Code: code, Message: d.Message, Details: map[string]string{"automation": d.Reason}, Cause: err}
	}
	return err
}

// ---- Service and HTTP ----

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

type Update struct {
	Mode                 string `json:"mode"`
	DailySubmissionLimit int    `json:"daily_submission_limit"`
}

var modes = map[string]bool{ModeDiscoveryOnly: true, ModeHumanApproved: true, ModeControlled: true}

func (s *Service) Update(ctx context.Context, userID uuid.UUID, in Update, ip string) (Settings, error) {
	errs := map[string]string{}
	if !modes[in.Mode] {
		errs["mode"] = "Choose DISCOVERY_ONLY, HUMAN_APPROVED or CONTROLLED."
	}
	if in.DailySubmissionLimit < 1 || in.DailySubmissionLimit > 200 {
		errs["daily_submission_limit"] = "Between 1 and 200."
	}
	if len(errs) > 0 {
		return Settings{}, httpx.Validation(errs)
	}
	err := db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO automation_settings (user_id, mode, daily_submission_limit) VALUES ($1, $2, $3)
			ON CONFLICT (user_id) DO UPDATE SET mode = EXCLUDED.mode, daily_submission_limit = EXCLUDED.daily_submission_limit,
			updated_at = now()`, userID, in.Mode, in.DailySubmissionLimit); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "automation.settings.update", SubjectType: "automation",
			Outcome: audit.Success, IP: ip, Metadata: map[string]any{"mode": in.Mode, "daily_submission_limit": in.DailySubmissionLimit}})
	})
	if err != nil {
		return Settings{}, err
	}
	return Load(ctx, s.pool, userID)
}

// SetPaused is the global kill switch. Pausing also cancels any submission
// attempt that automation (not the user) has in flight.
func (s *Service) SetPaused(ctx context.Context, userID uuid.UUID, paused bool, ip string) (Settings, error) {
	err := db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO automation_settings (user_id, paused, paused_at) VALUES ($1, $2, CASE WHEN $2 THEN now() END)
			ON CONFLICT (user_id) DO UPDATE SET paused = EXCLUDED.paused, paused_at = EXCLUDED.paused_at, updated_at = now()`,
			userID, paused); err != nil {
			return err
		}
		cancelled := int64(0)
		if paused {
			tag, err := tx.Exec(ctx, `UPDATE submission_attempts SET status = 'CANCELLED', finished_at = now(),
				error_message = 'Cancelled because automation was paused.'
				WHERE user_id = $1 AND status = 'PENDING' AND actor <> 'USER'`, userID)
			if err != nil {
				return err
			}
			cancelled = tag.RowsAffected()
		}
		action := "automation.resume"
		if paused {
			action = "automation.pause"
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: action, SubjectType: "automation", Outcome: audit.Success, IP: ip,
			Metadata: map[string]any{"cancelled_attempts": cancelled}})
	})
	if err != nil {
		return Settings{}, err
	}
	return Load(ctx, s.pool, userID)
}

type HTTP struct {
	svc   *Service
	authH *auth.HTTP
}

func NewHTTP(svc *Service, authH *auth.HTTP) *HTTP { return &HTTP{svc: svc, authH: authH} }

func (h *HTTP) Routes(r chi.Router) {
	r.Get("/automation/settings", h.get)
	r.Put("/automation/settings", h.put)
	r.Post("/automation/pause", h.pause(true))
	r.Post("/automation/resume", h.pause(false))
}

func (h *HTTP) get(w http.ResponseWriter, r *http.Request) {
	s, err := Load(r.Context(), h.svc.pool, auth.MustPrincipal(r.Context()).UserID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, s)
}

func (h *HTTP) put(w http.ResponseWriter, r *http.Request) {
	var in Update
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	s, err := h.svc.Update(r.Context(), auth.MustPrincipal(r.Context()).UserID, in, h.authH.ClientIP(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, s)
}

func (h *HTTP) pause(paused bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s, err := h.svc.SetPaused(r.Context(), auth.MustPrincipal(r.Context()).UserID, paused, h.authH.ClientIP(r))
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, s)
	}
}
