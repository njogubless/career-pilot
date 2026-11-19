// Package privacy implements the user's data rights: a complete export of
// their personal data, and account deletion (ADR-027). Both require the
// user's password. Retention choices here are engineering defaults pending
// legal review (KI-008).
package privacy

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/careerpilot/careerpilot/services/api/internal/audit"
	"github.com/careerpilot/careerpilot/services/api/internal/documents"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
)

// FormatVersion identifies the export layout.
const FormatVersion = "export-v1"

// ownerColumn maps each exported table to the column holding the user id.
// Every table with a foreign key to users must appear here or in
// notExported; a test enforces that, so new tables cannot be missed.
var ownerColumn = map[string]string{
	"users": "id", "user_preferences": "user_id", "professional_profiles": "user_id",
	"work_experiences": "user_id", "education_records": "user_id", "profile_skills": "user_id",
	"certifications": "user_id", "projects": "user_id", "profile_documents": "user_id",
	"profile_extractions": "user_id", "saved_searches": "user_id",
	"match_profiles": "user_id", "match_states": "user_id", "job_match_results": "user_id",
	"match_feedback": "user_id", "job_analysis_requests": "requested_by",
	"application_documents": "user_id", "application_document_versions": "user_id",
	"application_document_generations": "user_id", "application_document_approvals": "user_id",
	"applications": "user_id", "application_status_history": "user_id",
	"submission_attempts": "user_id", "submission_evidence": "user_id", "automation_settings": "user_id",
	"email_accounts": "user_id", "outreach_settings": "user_id", "recruiter_contacts": "user_id",
	"outreach_messages": "user_id", "outreach_replies": "user_id", "outreach_follow_ups": "user_id",
	"workflow_executions": "user_id", "workflow_tasks": "user_id", "approval_requests": "user_id",
	"agent_tool_calls": "user_id", "workflow_schedules": "user_id",
	"notification_preferences": "user_id", "notifications": "user_id", "career_reports": "user_id",
	"audit_events": "user_id",
}

// notExported are credentials, never personal content.
var notExported = map[string]string{
	"user_sessions":         "session credentials",
	"password_reset_tokens": "one-time credentials",
	"oauth_states":          "short-lived OAuth credentials",
}

// omitColumns are secrets or internal storage details left out of exports.
var omitColumns = map[string][]string{
	"users":             {"password_hash"},
	"email_accounts":    {"access_token_enc", "refresh_token_enc"},
	"profile_documents": {"storage_key"},
}

// OwnedTables and NotExported expose the coverage lists to tests.
func OwnedTables() map[string]string { return ownerColumn }
func NotExported() map[string]string { return notExported }

// Revoker revokes and removes a connected mailbox (Gmail). It may be nil.
type Revoker interface {
	Disconnect(ctx context.Context, userID uuid.UUID, ip string) error
}

type Service struct {
	pool  *pgxpool.Pool
	store documents.Storage
	gmail Revoker
	log   *slog.Logger
	now   func() time.Time
}

func NewService(pool *pgxpool.Pool, store documents.Storage, gmail Revoker, log *slog.Logger) *Service {
	return &Service{pool: pool, store: store, gmail: gmail, log: log, now: time.Now}
}

const readme = `CareerPilot AI data export (%s)

data.json   Every record CareerPilot stores about you, by table, as JSON.
documents/  The files you uploaded (CVs, evidence screenshots).

Not included:
- Passwords, session tokens and Gmail OAuth tokens (credentials, not content).
- Internal storage keys and vector embeddings (derived from your profile).
- Public job postings, which are not personal data; your matches and
  applications reference them by id and include their titles.

Generated %s.
`

// Export writes a zip archive of the user's data to w.
func (s *Service) Export(ctx context.Context, userID uuid.UUID, w io.Writer, ip string) error {
	tables, err := s.collect(ctx, userID)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	now := s.now().UTC()
	if f, err := zw.Create("README.txt"); err != nil {
		return err
	} else if _, err := fmt.Fprintf(f, readme, FormatVersion, now.Format(time.RFC3339)); err != nil {
		return err
	}
	f, err := zw.Create("data.json")
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]any{"format": FormatVersion, "exported_at": now, "user_id": userID, "tables": tables}); err != nil {
		return err
	}
	if s.store != nil {
		if err := s.writeDocuments(ctx, userID, zw); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return audit.Record(ctx, s.pool, audit.Event{UserID: &userID, Action: "account.export", Outcome: audit.Success, IP: ip,
		Metadata: map[string]any{"format": FormatVersion}})
}

// collect reads every owned table with one query each. Columns are listed
// from the catalogue, minus secrets and vector embeddings.
func (s *Service) collect(ctx context.Context, userID uuid.UUID) (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage, len(ownerColumn))
	for table, owner := range ownerColumn {
		cols, err := s.columns(ctx, table)
		if err != nil {
			return nil, err
		}
		if len(cols) == 0 {
			return nil, fmt.Errorf("export: table %s has no columns (schema drift?)", table)
		}
		quoted := make([]string, len(cols))
		for i, c := range cols {
			quoted[i] = pgx.Identifier{c}.Sanitize()
		}
		q := fmt.Sprintf(`SELECT coalesce(json_agg(row_to_json(t)), '[]'::json) FROM (SELECT %s FROM %s WHERE %s = $1) t`,
			strings.Join(quoted, ", "), pgx.Identifier{table}.Sanitize(), pgx.Identifier{owner}.Sanitize())
		var raw json.RawMessage
		if err := s.pool.QueryRow(ctx, q, userID).Scan(&raw); err != nil {
			return nil, fmt.Errorf("export %s: %w", table, err)
		}
		out[table] = raw
	}
	return out, nil
}

func (s *Service) columns(ctx context.Context, table string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT column_name FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = $1 AND udt_name <> 'vector' AND NOT (column_name = ANY($2))
		ORDER BY ordinal_position`, table, append([]string{}, omitColumns[table]...)) // never NULL: ANY(NULL) would drop every column
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func (s *Service) writeDocuments(ctx context.Context, userID uuid.UUID, zw *zip.Writer) error {
	rows, err := s.pool.Query(ctx, `SELECT id, original_filename, storage_key FROM profile_documents WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		return err
	}
	type doc struct {
		id        uuid.UUID
		name, key string
	}
	docs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (doc, error) {
		var d doc
		return d, r.Scan(&d.id, &d.name, &d.key)
	})
	if err != nil {
		return err
	}
	for _, d := range docs {
		data, err := s.store.Get(ctx, d.key)
		if err != nil {
			return fmt.Errorf("export document %s: %w", d.id, err)
		}
		// The id prefix keeps names unique; path.Base strips any directories.
		f, err := zw.Create("documents/" + d.id.String() + "-" + path.Base(documents.SanitizeFilename(d.name)))
		if err != nil {
			return err
		}
		if _, err := f.Write(data); err != nil {
			return err
		}
	}
	return nil
}

// DeleteResult summarises an account deletion.
type DeleteResult struct {
	AuditEventsPseudonymised int64
	ObjectsDeleted           int
	ObjectsLeftForSweep      int
	GmailRevoked             bool
}

// Delete permanently removes the account and everything it owns. Gmail
// access is revoked at Google first (best effort). The database deletion is
// one transaction: user-owned rows cascade, the user's audit events are
// pseudonymised, and an "account.deleted" event without personal data is
// recorded. Stored files are deleted afterwards; any that fail are removed
// by the orphan sweep.
func (s *Service) Delete(ctx context.Context, userID uuid.UUID) (DeleteResult, error) {
	var res DeleteResult
	if s.gmail != nil {
		if err := s.gmail.Disconnect(ctx, userID, ""); err == nil {
			res.GmailRevoked = true
		}
	}
	var keys []string
	err := db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT storage_key FROM profile_documents WHERE user_id = $1`, userID)
		if err != nil {
			return err
		}
		if keys, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return pgx.ErrNoRows
		}
		if err := tx.QueryRow(ctx, `SELECT audit_forget_user($1)`, userID).Scan(&res.AuditEventsPseudonymised); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "account.deleted", Outcome: audit.Success,
			Metadata: map[string]any{"documents": len(keys)}})
	})
	if err != nil {
		return res, err
	}
	for _, k := range keys {
		if s.store == nil {
			res.ObjectsLeftForSweep++
			continue
		}
		if err := s.store.Delete(context.WithoutCancel(ctx), k); err != nil {
			s.log.Warn("account deletion: object left for orphan sweep", "key", k, "err", err)
			res.ObjectsLeftForSweep++
			continue
		}
		res.ObjectsDeleted++
	}
	return res, nil
}
