package documents

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/careerpilot/careerpilot/services/api/internal/aiclient"
)

// ExtractCVArgs is the durable job payload. It holds IDs only: no personal
// data is copied into the queue.
type ExtractCVArgs struct {
	ExtractionID uuid.UUID `json:"extraction_id"`
	UserID       uuid.UUID `json:"user_id"`
}

func (ExtractCVArgs) Kind() string { return "extract_cv" }

func (ExtractCVArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 4}
}

// ExtractCVWorker sends the stored CV to the AI service and records the
// proposal for user review. Permanent failures (unreadable file, provider not
// configured, refusal) are not retried; transient ones are retried by River
// with backoff, and the record is marked FAILED after the final attempt.
type ExtractCVWorker struct {
	river.WorkerDefaults[ExtractCVArgs]
	Pool  *pgxpool.Pool
	Store Storage
	AI    *aiclient.Client
	Log   *slog.Logger
}

func (w *ExtractCVWorker) Timeout(*river.Job[ExtractCVArgs]) time.Duration { return 5 * time.Minute }

func (w *ExtractCVWorker) Work(ctx context.Context, job *river.Job[ExtractCVArgs]) error {
	a := job.Args
	var status, key, filename, mime string
	err := w.Pool.QueryRow(ctx, `
		SELECT e.status, d.storage_key, d.original_filename, d.mime_type
		FROM profile_extractions e JOIN profile_documents d ON d.id = e.document_id
		WHERE e.id = $1 AND e.user_id = $2`, a.ExtractionID, a.UserID).Scan(&status, &key, &filename, &mime)
	if errors.Is(err, pgx.ErrNoRows) {
		return river.JobCancel(fmt.Errorf("extraction %s no longer exists", a.ExtractionID))
	}
	if err != nil {
		return err
	}
	if status != "QUEUED" && status != "RUNNING" {
		return nil // already finished (idempotent on redelivery)
	}
	if _, err := w.Pool.Exec(ctx, `UPDATE profile_extractions SET status = 'RUNNING', updated_at = now() WHERE id = $1`, a.ExtractionID); err != nil {
		return err
	}

	if w.Store == nil || w.AI == nil {
		return w.fail(ctx, job, "SERVICE_NOT_CONFIGURED", "Document storage or the AI service is not configured on this server.",
			errors.New("worker missing storage or AI client"), false)
	}
	data, err := w.Store.Get(ctx, key)
	if err != nil {
		return w.fail(ctx, job, "DOCUMENT_UNAVAILABLE", "The stored document could not be read.", err, true)
	}
	resp, err := w.AI.ExtractCV(ctx, aiclient.ExtractCVRequest{
		Filename: filename, MimeType: mime, ContentBase64: base64.StdEncoding.EncodeToString(data),
	})
	if err != nil {
		var aerr *aiclient.Error
		if errors.As(err, &aerr) {
			return w.fail(ctx, job, aerr.Code, userMessage(aerr), err, aerr.Retryable)
		}
		return w.fail(ctx, job, "AI_SERVICE_ERROR", "The AI service failed.", err, true)
	}
	_, err = w.Pool.Exec(ctx, `UPDATE profile_extractions SET status = 'PENDING_REVIEW', proposal = $2, provider = $3, model = $4,
		prompt_version = $5, input_tokens = $6, output_tokens = $7, error_code = NULL, error_message = NULL, updated_at = now()
		WHERE id = $1`, a.ExtractionID, []byte(resp.Proposal), resp.Meta.Provider, resp.Meta.Model, resp.Meta.PromptVersion,
		resp.Meta.Usage.InputTokens, resp.Meta.Usage.OutputTokens)
	return err
}

// fail records the failure. Retryable errors are returned to River unless this
// was the final attempt; permanent errors cancel the job.
func (w *ExtractCVWorker) fail(ctx context.Context, job *river.Job[ExtractCVArgs], code, msg string, cause error, retryable bool) error {
	final := !retryable || job.Attempt >= job.MaxAttempts
	if final {
		if _, err := w.Pool.Exec(context.WithoutCancel(ctx), `UPDATE profile_extractions SET status = 'FAILED', error_code = $2,
			error_message = $3, updated_at = now() WHERE id = $1`, job.Args.ExtractionID, code, msg); err != nil {
			w.Log.Error("record extraction failure", "err", err)
		}
		w.Log.Warn("cv extraction failed", "extraction_id", job.Args.ExtractionID, "code", code, "attempt", job.Attempt, "err", cause)
		return river.JobCancel(cause)
	}
	w.Log.Warn("cv extraction attempt failed; will retry", "extraction_id", job.Args.ExtractionID, "code", code, "attempt", job.Attempt, "err", cause)
	return cause
}

func userMessage(e *aiclient.Error) string {
	switch e.Code {
	case "UNREADABLE_DOCUMENT":
		return "The document could not be read. It may be scanned, encrypted or empty. Try another file or enter details manually."
	case "AI_PROVIDER_UNAVAILABLE":
		return "No AI provider is configured, so the CV could not be analysed."
	case "AI_REFUSED":
		return "The AI provider declined to process this document."
	case "AI_PROVIDER_TRANSIENT", "AI_SERVICE_UNREACHABLE":
		return "The AI service was temporarily unavailable. Please try again."
	}
	if e.Message != "" && e.Status >= 400 && e.Status < 500 {
		return e.Message
	}
	return "The CV could not be analysed."
}
