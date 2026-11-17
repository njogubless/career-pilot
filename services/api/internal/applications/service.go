package applications

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/careerpilot/careerpilot/services/api/internal/audit"
	"github.com/careerpilot/careerpilot/services/api/internal/automation"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
)

type Service struct {
	pool         *pgxpool.Pool
	guard        *automation.Guard
	submitters   map[string]Submitter
	matchVersion string
	log          *slog.Logger
	now          func() time.Time
}

func NewService(pool *pgxpool.Pool, guard *automation.Guard, matchVersion string, log *slog.Logger) *Service {
	return &Service{pool: pool, guard: guard, matchVersion: matchVersion, log: log, now: time.Now,
		submitters: map[string]Submitter{ChannelAssisted: Assisted{}, ChannelEmail: EmailUnavailable{}}}
}

var (
	errNotFound = httpx.NewError(httpx.CodeNotFound, "Application not found.")
	maxNotes    = 5000
	maxReason   = 1000
)

// appRow is the locked state of one application.
type appRow struct {
	ID, JobID               uuid.UUID
	Status                  string
	Channel                 *string
	CV, Cover, Answers      *uuid.UUID
	ApprovedAt, SubmittedAt *time.Time
}

func lockApp(ctx context.Context, tx pgx.Tx, userID, id uuid.UUID) (appRow, error) {
	var a appRow
	err := tx.QueryRow(ctx, `SELECT id, job_id, status, channel, cv_version_id, cover_letter_version_id, answers_version_id,
		approved_at, submitted_at FROM applications WHERE id = $1 AND user_id = $2 FOR UPDATE`, id, userID).Scan(
		&a.ID, &a.JobID, &a.Status, &a.Channel, &a.CV, &a.Cover, &a.Answers, &a.ApprovedAt, &a.SubmittedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, errNotFound
	}
	return a, err
}

// setStatus moves an application along a valid edge and records history.
// Guarded edges are only allowed when the caller is the dedicated action.
func setStatus(ctx context.Context, tx pgx.Tx, userID uuid.UUID, a *appRow, to string, actor audit.Actor, reason string, guardedOK bool) error {
	valid, guarded := CanTransition(a.Status, to)
	if !valid || (guarded && !guardedOK) {
		return &httpx.Error{Code: httpx.CodeInvalidTransition,
			Message: fmt.Sprintf("An application cannot move from %s to %s.", a.Status, to),
			Details: map[string]string{"from": a.Status, "to": to}}
	}
	if _, err := tx.Exec(ctx, `UPDATE applications SET status = $2, updated_at = now() WHERE id = $1`, a.ID, to); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO application_status_history (id, application_id, user_id, from_status, to_status, actor, reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, ids.New(), a.ID, userID, a.Status, to, actor, reason); err != nil {
		return err
	}
	a.Status = to
	return nil
}

func cancelPending(ctx context.Context, tx pgx.Tx, appID uuid.UUID, why string) error {
	_, err := tx.Exec(ctx, `UPDATE submission_attempts SET status = 'CANCELLED', finished_at = now(), error_message = $2
		WHERE application_id = $1 AND status = 'PENDING'`, appID, why)
	return err
}

func clearApproval(ctx context.Context, tx pgx.Tx, appID uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE applications SET cv_version_id = NULL, cover_letter_version_id = NULL, answers_version_id = NULL,
		channel = NULL, approved_at = NULL WHERE id = $1`, appID)
	return err
}

func cleanText(field, v string, max int) (string, error) {
	v = strings.TrimSpace(v)
	if utf8.RuneCountInString(v) > max {
		return "", httpx.Validation(map[string]string{field: fmt.Sprintf("Use at most %d characters.", max)})
	}
	return v, nil
}

type CreateInput struct {
	JobID  uuid.UUID `json:"job_id"`
	Status string    `json:"status"`
	Notes  string    `json:"notes"`
}

// Create starts tracking a job. One application per job per user; postings
// collapsed as cross-source duplicates count as the same job.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, in CreateInput, ip string) (uuid.UUID, error) {
	if in.Status == "" {
		in.Status = Shortlisted
	}
	if in.Status != Discovered && in.Status != Shortlisted {
		return uuid.Nil, httpx.Validation(map[string]string{"status": "New applications start as DISCOVERED or SHORTLISTED."})
	}
	notes, err := cleanText("notes", in.Notes, maxNotes)
	if err != nil {
		return uuid.Nil, err
	}
	id := ids.New()
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var canonical uuid.UUID
		err := tx.QueryRow(ctx, `SELECT coalesce(duplicate_of, id) FROM jobs WHERE id = $1`, in.JobID).Scan(&canonical)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewError(httpx.CodeNotFound, "Job not found.")
		}
		if err != nil {
			return err
		}
		var existing uuid.UUID
		err = tx.QueryRow(ctx, `SELECT a.id FROM applications a JOIN jobs j ON j.id = a.job_id
			WHERE a.user_id = $1 AND (a.job_id = $2 OR coalesce(j.duplicate_of, j.id) = $3) LIMIT 1`, userID, in.JobID, canonical).Scan(&existing)
		if err == nil {
			return &httpx.Error{Code: httpx.CodeConflict, Message: "You already track an application for this job.",
				Details: map[string]string{"application_id": existing.String()}}
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO applications (id, user_id, job_id, status, notes) VALUES ($1, $2, $3, $4, $5)`,
			id, userID, in.JobID, in.Status, notes); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return httpx.NewError(httpx.CodeConflict, "You already track an application for this job.")
			}
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO application_status_history (id, application_id, user_id, from_status, to_status, actor, reason)
			VALUES ($1, $2, $3, NULL, $4, 'USER', 'Created')`, ids.New(), id, userID, in.Status); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "application.create", SubjectType: "application", SubjectID: &id,
			Outcome: audit.Success, IP: ip, Metadata: map[string]any{"job_id": in.JobID, "status": in.Status}})
	})
	return id, err
}

type TransitionInput struct {
	To     string `json:"to"`
	Reason string `json:"reason"`
}

// Transition applies a plain status change requested by the user.
func (s *Service) Transition(ctx context.Context, userID, id uuid.UUID, in TransitionInput, ip string) error {
	if !IsValid(in.To) {
		return httpx.Validation(map[string]string{"to": "Unknown status."})
	}
	reason, err := cleanText("reason", in.Reason, maxReason)
	if err != nil {
		return err
	}
	return db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		a, err := lockApp(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		from := a.Status
		if in.To == AwaitingApproval {
			pkg, err := loadPackage(ctx, tx, userID, a.JobID)
			if err != nil {
				return err
			}
			if !pkg.Ready {
				return &httpx.Error{Code: httpx.CodeValidationFailed, Message: strings.Join(pkg.Blockers, " "),
					Details: map[string]string{"package": "NOT_READY"}}
			}
		}
		if err := setStatus(ctx, tx, userID, &a, in.To, audit.ActorUser, reason, false); err != nil {
			return err
		}
		switch {
		case in.To == Withdrawn:
			if err := cancelPending(ctx, tx, a.ID, "Cancelled because the application was withdrawn."); err != nil {
				return err
			}
		case in.To == Preparing && (from == Approved || from == Failed):
			// Reopening discards the approval; the package must be approved again.
			if err := cancelPending(ctx, tx, a.ID, "Cancelled because the application was reopened."); err != nil {
				return err
			}
			if err := clearApproval(ctx, tx, a.ID); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "application.transition", SubjectType: "application", SubjectID: &id,
			Outcome: audit.Success, IP: ip, Metadata: map[string]any{"from": from, "to": in.To}})
	})
}

type ApproveInput struct {
	Channel string `json:"channel"`
	// Confirm is the user's explicit authorisation to submit this package.
	Confirm bool `json:"confirm"`
}

// Approve pins the approved document versions and authorises submission via
// the chosen channel (AWAITING_APPROVAL → APPROVED).
func (s *Service) Approve(ctx context.Context, userID, id uuid.UUID, in ApproveInput, ip string) error {
	if !in.Confirm {
		return httpx.Validation(map[string]string{"confirm": "Confirm that you authorise this application to be submitted."})
	}
	sub, ok := s.submitters[in.Channel]
	if !ok {
		return httpx.Validation(map[string]string{"channel": "Choose ASSISTED or EMAIL."})
	}
	if avail, detail := sub.Availability(); !avail {
		return &httpx.Error{Code: httpx.CodeValidationFailed, Message: detail, Details: map[string]string{"channel": detail}}
	}
	return db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		a, err := lockApp(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		if a.Status != AwaitingApproval {
			return httpx.NewError(httpx.CodeInvalidTransition, "Only applications awaiting approval can be approved.")
		}
		pkg, err := loadPackage(ctx, tx, userID, a.JobID)
		if err != nil {
			return err
		}
		if !pkg.Ready {
			return &httpx.Error{Code: httpx.CodeValidationFailed, Message: strings.Join(pkg.Blockers, " "),
				Details: map[string]string{"package": "NOT_READY"}}
		}
		pins := pkg.pins()
		if _, err := tx.Exec(ctx, `UPDATE applications SET cv_version_id = $2, cover_letter_version_id = $3, answers_version_id = $4,
			channel = $5, approved_at = now() WHERE id = $1`, a.ID, pins[kindCV], pins[kindCover], pins[kindAnswers], in.Channel); err != nil {
			return err
		}
		if err := setStatus(ctx, tx, userID, &a, Approved, audit.ActorUser, "Package approved for "+strings.ToLower(in.Channel)+" submission", true); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "application.approve", SubjectType: "application", SubjectID: &id,
			Outcome: audit.Success, IP: ip, Metadata: map[string]any{"channel": in.Channel, "cv_version_id": pins[kindCV],
				"cover_letter_version_id": pins[kindCover], "answers_version_id": pins[kindAnswers]}})
	})
}

// RevokeApproval withdraws the authorisation (APPROVED/FAILED →
// AWAITING_APPROVAL) and cancels any attempt in flight.
func (s *Service) RevokeApproval(ctx context.Context, userID, id uuid.UUID, ip string) error {
	return db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		a, err := lockApp(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		if a.Status != Approved && a.Status != Failed {
			return httpx.NewError(httpx.CodeInvalidTransition, "This application has no approval to revoke.")
		}
		if err := cancelPending(ctx, tx, a.ID, "Cancelled because the approval was revoked."); err != nil {
			return err
		}
		// Status first: the table requires pins while APPROVED or FAILED.
		if err := setStatus(ctx, tx, userID, &a, AwaitingApproval, audit.ActorUser, "Approval revoked", true); err != nil {
			return err
		}
		if err := clearApproval(ctx, tx, a.ID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "application.approval.revoke", SubjectType: "application",
			SubjectID: &id, Outcome: audit.Success, IP: ip})
	})
}

func (s *Service) UpdateNotes(ctx context.Context, userID, id uuid.UUID, notes string) error {
	notes, err := cleanText("notes", notes, maxNotes)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE applications SET notes = $3, updated_at = now() WHERE id = $1 AND user_id = $2`, id, userID, notes)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errNotFound
	}
	return nil
}

// Delete removes an application that was never submitted. Submitted
// applications are history: withdraw them instead.
func (s *Service) Delete(ctx context.Context, userID, id uuid.UUID, ip string) error {
	return db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		a, err := lockApp(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		if a.SubmittedAt != nil {
			return httpx.NewError(httpx.CodeInvalidTransition, "Submitted applications are kept as history. Withdraw it instead.")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM applications WHERE id = $1`, a.ID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "application.delete", SubjectType: "application", SubjectID: &id,
			Outcome: audit.Success, IP: ip, Metadata: map[string]any{"status": a.Status, "job_id": a.JobID}})
	})
}
