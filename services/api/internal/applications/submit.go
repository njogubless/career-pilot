package applications

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/careerpilot/careerpilot/services/api/internal/audit"
	"github.com/careerpilot/careerpilot/services/api/internal/automation"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
)

const (
	ChannelAssisted = "ASSISTED"
	ChannelEmail    = "EMAIL"
)

// Submitter is a submission channel (ADR-007, ADR-022). A channel never
// reports success by itself: a submission only counts once evidence is
// recorded.
type Submitter interface {
	// Availability reports whether the channel can be used, with a reason if not.
	Availability() (bool, string)
	// RequiresUser reports channels where the user performs the submission.
	RequiresUser() bool
}

// Assisted: the user submits on the employer's official site with the
// approved package, then confirms. CareerPilot never fills or submits
// third-party forms, so CAPTCHA, MFA and bot protections are never touched.
type Assisted struct{}

func (Assisted) Availability() (bool, string) { return true, "" }
func (Assisted) RequiresUser() bool           { return true }

// EmailUnavailable: email applications need a connected Gmail account (Phase 7).
type EmailUnavailable struct{}

func (EmailUnavailable) Availability() (bool, string) {
	return false, "Email applications need a connected Gmail account, which arrives in Phase 7."
}
func (EmailUnavailable) RequiresUser() bool { return false }

type Attempt struct {
	ID             uuid.UUID  `json:"id"`
	Channel        string     `json:"channel"`
	Actor          string     `json:"actor"`
	Status         string     `json:"status"`
	IdempotencyKey string     `json:"idempotency_key"`
	FailureReason  *string    `json:"failure_reason"`
	ErrorMessage   *string    `json:"error_message"`
	StartedAt      time.Time  `json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at"`
}

const attemptCols = `id, channel, actor, status, idempotency_key, failure_reason, error_message, started_at, finished_at`

func scanAttempt(row pgx.Row) (Attempt, error) {
	var a Attempt
	err := row.Scan(&a.ID, &a.Channel, &a.Actor, &a.Status, &a.IdempotencyKey, &a.FailureReason, &a.ErrorMessage, &a.StartedAt, &a.FinishedAt)
	return a, err
}

var keyRe = regexp.MustCompile(`^[A-Za-z0-9_.:-]{8,100}$`)

// pinsStillApproved checks that every pinned version is still its
// document's approved version (the user may have revoked or replaced it).
func pinsStillApproved(ctx context.Context, q db.Querier, a appRow) (bool, error) {
	pins := []uuid.UUID{}
	for _, p := range []*uuid.UUID{a.CV, a.Cover, a.Answers} {
		if p != nil {
			pins = append(pins, *p)
		}
	}
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM application_documents WHERE approved_version_id = ANY($1)`, pins).Scan(&n)
	return n == len(pins) && len(pins) > 0, err
}

// StartSubmission opens a submission attempt for an approved application.
// It is idempotent per key: replaying the same key returns the same attempt.
// Permissions are checked immediately before the attempt is recorded.
func (s *Service) StartSubmission(ctx context.Context, userID, id uuid.UUID, key string, actor audit.Actor, ip string) (Attempt, bool, error) {
	if !keyRe.MatchString(key) {
		return Attempt{}, false, httpx.Validation(map[string]string{"idempotency_key": "Send an Idempotency-Key header of 8–100 characters."})
	}
	var att Attempt
	created := false
	err := db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		a, err := lockApp(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		var existingApp uuid.UUID
		err = tx.QueryRow(ctx, `SELECT application_id FROM submission_attempts WHERE user_id = $1 AND idempotency_key = $2`, userID, key).Scan(&existingApp)
		if err == nil {
			if existingApp != a.ID {
				return httpx.NewError(httpx.CodeConflict, "This idempotency key was already used for another application.")
			}
			att, err = scanAttempt(tx.QueryRow(ctx, `SELECT `+attemptCols+` FROM submission_attempts WHERE user_id = $1 AND idempotency_key = $2`, userID, key))
			return err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if a.Status != Approved {
			return httpx.NewError(httpx.CodeInvalidTransition, "Only approved applications can be submitted.")
		}
		sub, ok := s.submitters[deref(a.Channel)]
		if !ok {
			return httpx.NewError(httpx.CodeInvalidTransition, "This application has no submission channel. Approve it again.")
		}
		if avail, detail := sub.Availability(); !avail {
			return httpx.NewError(httpx.CodeUnavailable, detail)
		}
		if sub.RequiresUser() && actor != audit.ActorUser {
			return automation.AsHTTP(&automation.Denied{Reason: "CHANNEL", Message: "Assisted submissions are performed by you on the employer's site."})
		}
		ok, err = pinsStillApproved(ctx, tx, a)
		if err != nil {
			return err
		}
		if !ok {
			return httpx.NewError(httpx.CodeConflict, "The approved documents changed or were un-approved after this application was approved. Review and approve it again.")
		}
		if err := s.guard.Authorize(ctx, tx, userID, actor, automation.ActionSubmit); err != nil {
			return automation.AsHTTP(err)
		}
		att, err = scanAttempt(tx.QueryRow(ctx, `INSERT INTO submission_attempts (id, application_id, user_id, channel, actor, status, idempotency_key)
			VALUES ($1, $2, $3, $4, $5, 'PENDING', $6) RETURNING `+attemptCols, ids.New(), a.ID, userID, *a.Channel, actor, key))
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return httpx.NewError(httpx.CodeConflict, "A submission for this application is already in progress. Confirm or cancel it first.")
		}
		if err != nil {
			return err
		}
		created = true
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Actor: actor, Action: "application.submission.start", SubjectType: "application",
			SubjectID: &id, Outcome: audit.Success, IP: ip, Metadata: map[string]any{"attempt_id": att.ID, "channel": att.Channel}})
	})
	return att, created, err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// lockAttempt loads a PENDING attempt of a locked application.
func lockAttempt(ctx context.Context, tx pgx.Tx, appID, attemptID uuid.UUID) (Attempt, error) {
	att, err := scanAttempt(tx.QueryRow(ctx, `SELECT `+attemptCols+` FROM submission_attempts WHERE id = $1 AND application_id = $2 FOR UPDATE`,
		attemptID, appID))
	if errors.Is(err, pgx.ErrNoRows) {
		return att, httpx.NewError(httpx.CodeNotFound, "Submission attempt not found.")
	}
	if err != nil {
		return att, err
	}
	if att.Status != "PENDING" {
		return att, httpx.NewError(httpx.CodeInvalidTransition, "This submission attempt is already "+att.Status+".")
	}
	return att, nil
}

type EvidenceInput struct {
	// Attest is the user's statement that they submitted the application.
	Attest     bool       `json:"attest"`
	Type       string     `json:"type"`
	Reference  string     `json:"reference"`
	Note       string     `json:"note"`
	DocumentID *uuid.UUID `json:"document_id"`
	OccurredAt *time.Time `json:"occurred_at"`
}

// ConfirmSubmission records evidence and only then marks the application
// SUBMITTED. Users can only attest (USER_ATTESTED); email and ATS
// confirmations are recorded by integrations (Phase 7+).
func (s *Service) ConfirmSubmission(ctx context.Context, userID, id, attemptID uuid.UUID, in EvidenceInput, ip string) error {
	errs := map[string]string{}
	if !in.Attest {
		errs["attest"] = "Confirm that you submitted this application on the employer's site."
	}
	if in.Type == "" {
		in.Type = "USER_ATTESTED"
	}
	if in.Type != "USER_ATTESTED" {
		errs["type"] = "Only USER_ATTESTED evidence can be recorded by hand; email and ATS confirmations come from integrations."
	}
	ref, err := cleanText("reference", in.Reference, 500)
	if err != nil {
		return err
	}
	note, err := cleanText("note", in.Note, 2000)
	if err != nil {
		return err
	}
	now := s.now()
	occurred := now
	if in.OccurredAt != nil {
		occurred = *in.OccurredAt
		if occurred.After(now.Add(5 * time.Minute)) {
			errs["occurred_at"] = "The submission time cannot be in the future."
		}
	}
	if len(errs) > 0 {
		return httpx.Validation(errs)
	}
	return db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		a, err := lockApp(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		att, err := lockAttempt(ctx, tx, a.ID, attemptID)
		if err != nil {
			return err
		}
		if occurred.Before(att.StartedAt.Add(-24 * time.Hour)) {
			return httpx.Validation(map[string]string{"occurred_at": "The submission time must be close to when you started this attempt."})
		}
		if in.DocumentID != nil {
			var owned bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM profile_documents WHERE id = $1 AND user_id = $2)`, *in.DocumentID, userID).Scan(&owned); err != nil {
				return err
			}
			if !owned {
				return httpx.Validation(map[string]string{"document_id": "Attach one of your uploaded documents."})
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE submission_attempts SET status = 'SUCCEEDED', finished_at = now() WHERE id = $1`, att.ID); err != nil {
			return err
		}
		evID := ids.New()
		if _, err := tx.Exec(ctx, `INSERT INTO submission_evidence (id, application_id, attempt_id, user_id, type, reference, note, document_id, occurred_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, evID, a.ID, att.ID, userID, in.Type, ref, note, in.DocumentID, occurred); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE applications SET submitted_at = $2 WHERE id = $1`, a.ID, occurred); err != nil {
			return err
		}
		if err := setStatus(ctx, tx, userID, &a, Submitted, audit.ActorUser, "Submission confirmed by the user ("+in.Type+")", true); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "application.submission.confirm", SubjectType: "application",
			SubjectID: &id, Outcome: audit.Success, IP: ip, Metadata: map[string]any{"attempt_id": att.ID, "evidence_id": evID,
				"evidence_type": in.Type, "has_reference": ref != "", "has_attachment": in.DocumentID != nil}})
	})
}

var failureReasons = map[string]string{
	"SITE_ERROR":            "The employer's site returned an error.",
	"POSTING_CLOSED":        "The posting is closed.",
	"VERIFICATION_REQUIRED": "The site required a human verification step (CAPTCHA, MFA or login).",
	"INELIGIBLE":            "The site's own screening showed you are not eligible.",
	"OTHER":                 "The submission did not go through.",
}

type FailureInput struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// FailSubmission records a failed attempt; the application becomes FAILED
// (it can be retried or reopened). Failures are never hidden.
func (s *Service) FailSubmission(ctx context.Context, userID, id, attemptID uuid.UUID, in FailureInput, ip string) error {
	if _, ok := failureReasons[in.Reason]; !ok {
		return httpx.Validation(map[string]string{"reason": "Choose SITE_ERROR, POSTING_CLOSED, VERIFICATION_REQUIRED, INELIGIBLE or OTHER."})
	}
	msg, err := cleanText("message", in.Message, maxReason)
	if err != nil {
		return err
	}
	if msg == "" {
		msg = failureReasons[in.Reason]
	}
	return db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		a, err := lockApp(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		att, err := lockAttempt(ctx, tx, a.ID, attemptID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE submission_attempts SET status = 'FAILED', failure_reason = $2, error_message = $3, finished_at = now()
			WHERE id = $1`, att.ID, in.Reason, msg); err != nil {
			return err
		}
		if err := setStatus(ctx, tx, userID, &a, Failed, audit.ActorUser, fmt.Sprintf("Submission failed: %s", msg), true); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "application.submission.fail", SubjectType: "application",
			SubjectID: &id, Outcome: audit.Failure, IP: ip, Metadata: map[string]any{"attempt_id": att.ID, "reason": in.Reason}})
	})
}

// CancelSubmission abandons an attempt without changing the application.
func (s *Service) CancelSubmission(ctx context.Context, userID, id, attemptID uuid.UUID, ip string) error {
	return db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		a, err := lockApp(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		att, err := lockAttempt(ctx, tx, a.ID, attemptID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE submission_attempts SET status = 'CANCELLED', finished_at = now(), error_message = 'Cancelled by the user.'
			WHERE id = $1`, att.ID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "application.submission.cancel", SubjectType: "application",
			SubjectID: &id, Outcome: audit.Success, IP: ip, Metadata: map[string]any{"attempt_id": att.ID}})
	})
}
