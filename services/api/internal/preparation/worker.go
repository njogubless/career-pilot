package preparation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/careerpilot/careerpilot/services/api/internal/aiclient"
	"github.com/careerpilot/careerpilot/services/api/internal/audit"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
)

const (
	maxJobChars = 60_000
	errGeneric  = "The draft could not be generated."
)

// GenerateDocumentArgs is the durable job payload: IDs only, no personal data.
type GenerateDocumentArgs struct {
	GenerationID uuid.UUID `json:"generation_id"`
	UserID       uuid.UUID `json:"user_id"`
}

func (GenerateDocumentArgs) Kind() string { return "generate_application_document" }

func (GenerateDocumentArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 3} }

// GenerateDocumentWorker builds the fact sheet, asks the AI service for a
// grounded draft, renders structural text from the profile and stores the
// result as a new GENERATED version. Nothing is sent anywhere.
type GenerateDocumentWorker struct {
	river.WorkerDefaults[GenerateDocumentArgs]
	Service *Service
}

func (w *GenerateDocumentWorker) Timeout(*river.Job[GenerateDocumentArgs]) time.Duration {
	return 5 * time.Minute
}

func (w *GenerateDocumentWorker) Work(ctx context.Context, job *river.Job[GenerateDocumentArgs]) error {
	s, a := w.Service, job.Args
	var status string
	var docID, jobID uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT g.status, g.document_id, d.job_id FROM application_document_generations g
		JOIN application_documents d ON d.id = g.document_id WHERE g.id = $1 AND g.user_id = $2`,
		a.GenerationID, a.UserID).Scan(&status, &docID, &jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return river.JobCancel(fmt.Errorf("generation %s no longer exists", a.GenerationID))
	}
	if err != nil {
		return err
	}
	if status != "QUEUED" && status != "RUNNING" {
		return nil // finished already (idempotent on redelivery)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE application_document_generations SET status = 'RUNNING', updated_at = now() WHERE id = $1`,
		a.GenerationID); err != nil {
		return err
	}
	if s.ai == nil {
		return w.fail(ctx, job, "SERVICE_NOT_CONFIGURED", "The AI service is not configured on this server.", errors.New("no ai client"), false)
	}

	d, err := getDoc(ctx, s.pool, a.UserID, docID)
	if err != nil {
		return w.fail(ctx, job, "DOCUMENT_UNAVAILABLE", "The document no longer exists.", err, false)
	}
	sh, err := s.loadSheet(ctx, a.UserID, d.Notes)
	if err != nil {
		return w.fail(ctx, job, "INTERNAL", errGeneric, err, true)
	}
	if countUsable(sh) == 0 {
		return w.fail(ctx, job, "PROFILE_INCOMPLETE",
			"Your profile has no verified experience, skills or education to build from. Add or verify facts on your profile first.",
			errors.New("no verified facts"), false)
	}
	jc, err := jobContext(ctx, s, jobID)
	if err != nil {
		return w.fail(ctx, job, "INTERNAL", errGeneric, err, true)
	}
	resp, err := s.ai.GenerateDocument(ctx, aiclient.GenerateDocumentRequest{Kind: d.Kind, Job: jc, Facts: sh.Facts,
		Candidate: sh.Candidate, Questions: d.Questions})
	if err != nil {
		var aerr *aiclient.Error
		if errors.As(err, &aerr) {
			return w.fail(ctx, job, aerr.Code, userMessage(aerr), err, aerr.Retryable)
		}
		return w.fail(ctx, job, "AI_SERVICE_ERROR", "The AI service failed.", err, true)
	}
	var c Content
	if err := json.Unmarshal(resp.Content, &c); err != nil {
		return w.fail(ctx, job, "AI_OUTPUT_INVALID", "The AI service returned an unreadable document.", err, true)
	}
	if err := c.sanitize(d.Kind); err != nil {
		return w.fail(ctx, job, "AI_OUTPUT_INVALID", "The AI service returned an invalid document.", err, true)
	}
	sh.render(&c)
	val := resp.Validation
	if val.Issues == nil {
		val.Issues = []aiclient.Issue{}
	}
	usage := resp.Usage

	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		vid, _, err := insertVersion(ctx, tx, a.UserID, docID, newVersion{Origin: OriginGenerated, Content: c, Validation: &val,
			Questions: resp.Questions, Warnings: resp.Warnings, Usage: &usage, FactsHash: sh.Hash})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE application_document_generations SET status = 'SUCCEEDED', version_id = $2,
			error_code = NULL, error_message = NULL, updated_at = now() WHERE id = $1`, a.GenerationID, vid); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &a.UserID, Actor: audit.ActorAgent, Action: "application_document.generate",
			SubjectType: "application_document", SubjectID: &docID, Outcome: audit.Success,
			Metadata: map[string]any{"kind": d.Kind, "version_id": vid, "validation": val.Status, "provider": usage.Provider,
				"model": usage.Model, "prompt_version": usage.PromptVersion, "input_tokens": usage.InputTokens, "output_tokens": usage.OutputTokens}})
	})
	if err != nil {
		return w.fail(ctx, job, "INTERNAL", errGeneric, err, true)
	}
	return nil
}

func countUsable(sh sheet) int {
	n := 0
	for _, f := range sh.Facts {
		switch f.Kind {
		case "experience", "education", "skill", "project", "certification":
			n++
		}
	}
	return n
}

func jobContext(ctx context.Context, s *Service, jobID uuid.UUID) (aiclient.JobContext, error) {
	var jc aiclient.JobContext
	if err := s.pool.QueryRow(ctx, `SELECT title, company_name, left(description_text, $2) FROM jobs WHERE id = $1`, jobID, maxJobChars).
		Scan(&jc.Title, &jc.Company, &jc.Text); err != nil {
		return jc, err
	}
	jc.Skills = []string{}
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT analysis FROM job_analyses WHERE job_id = $1 ORDER BY (method = 'LLM') DESC LIMIT 1`, jobID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return jc, nil
	}
	if err != nil {
		return jc, err
	}
	var a struct {
		Skills []struct {
			Name string `json:"name"`
		} `json:"skills"`
	}
	if json.Unmarshal(raw, &a) == nil {
		for _, sk := range a.Skills {
			if len(jc.Skills) < 60 {
				jc.Skills = append(jc.Skills, sk.Name)
			}
		}
	}
	return jc, nil
}

// fail records the failure; retryable errors go back to River unless this was
// the final attempt.
func (w *GenerateDocumentWorker) fail(ctx context.Context, job *river.Job[GenerateDocumentArgs], code, msg string, cause error, retryable bool) error {
	s := w.Service
	if retryable && job.Attempt < job.MaxAttempts {
		s.log.Warn("document generation attempt failed; will retry", "generation_id", job.Args.GenerationID, "code", code, "attempt", job.Attempt, "err", cause)
		return cause
	}
	if _, err := s.pool.Exec(context.WithoutCancel(ctx), `UPDATE application_document_generations SET status = 'FAILED', error_code = $2,
		error_message = $3, updated_at = now() WHERE id = $1`, job.Args.GenerationID, code, msg); err != nil {
		s.log.Error("record generation failure", "err", err)
	}
	s.log.Warn("document generation failed", "generation_id", job.Args.GenerationID, "code", code, "attempt", job.Attempt, "err", cause)
	return river.JobCancel(cause)
}

func userMessage(e *aiclient.Error) string {
	switch e.Code {
	case "AI_PROVIDER_UNAVAILABLE":
		return "No AI provider is configured, so the draft could not be generated."
	case "AI_REFUSED":
		return "The AI provider declined to write this document."
	case "AI_PROVIDER_TRANSIENT", "AI_SERVICE_UNREACHABLE":
		return "The AI service was temporarily unavailable. Please try again."
	case "AI_OUTPUT_INVALID":
		return "The AI produced an incomplete draft. Please try again."
	}
	return errGeneric
}
