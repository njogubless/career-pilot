// Package documents handles private document uploads (CVs, evidence) and CV
// extraction into reviewable profile proposals.
package documents

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/careerpilot/careerpilot/services/api/internal/aiclient"
	"github.com/careerpilot/careerpilot/services/api/internal/audit"
	"github.com/careerpilot/careerpilot/services/api/internal/jobs"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
	"github.com/careerpilot/careerpilot/services/api/internal/profiles"
)

const (
	MimePDF  = "application/pdf"
	MimeDOCX = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
)

type Document struct {
	ID               uuid.UUID `json:"id"`
	Kind             string    `json:"kind"`
	OriginalFilename string    `json:"original_filename"`
	MimeType         string    `json:"mime_type"`
	SizeBytes        int64     `json:"size_bytes"`
	CreatedAt        time.Time `json:"created_at"`
	storageKey       string
}

type Extraction struct {
	ID            uuid.UUID       `json:"id"`
	DocumentID    uuid.UUID       `json:"document_id"`
	Status        string          `json:"status"`
	Proposal      json.RawMessage `json:"proposal,omitempty"`
	ErrorCode     *string         `json:"error_code,omitempty"`
	ErrorMessage  *string         `json:"error_message,omitempty"`
	Provider      *string         `json:"provider,omitempty"`
	Model         *string         `json:"model,omitempty"`
	PromptVersion *string         `json:"prompt_version,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type Service struct {
	pool    *pgxpool.Pool
	store   Storage // nil when storage is not configured
	jobs    *jobs.Client
	ai      *aiclient.Client // nil when the AI service is not configured
	maxSize int64
	log     *slog.Logger
}

func NewService(pool *pgxpool.Pool, store Storage, jobsClient *jobs.Client, ai *aiclient.Client, maxSize int64, log *slog.Logger) *Service {
	return &Service{pool: pool, store: store, jobs: jobsClient, ai: ai, maxSize: maxSize, log: log}
}

var (
	errStorageUnavailable = httpx.NewError(httpx.CodeUnavailable, "Document storage is not configured on this server.")
	errDocNotFound        = httpx.NewError(httpx.CodeNotFound, "Document not found.")
	errExtractionNotFound = httpx.NewError(httpx.CodeNotFound, "Extraction not found.")
)

// DetectType validates content by magic bytes and structure, not by the
// client-supplied Content-Type. Only PDF and DOCX are accepted.
func DetectType(data []byte, filename string) (string, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	switch {
	case bytes.HasPrefix(data, []byte("%PDF-")):
		if ext != ".pdf" {
			return "", errors.New("file content is PDF but the extension is not .pdf")
		}
		return MimePDF, nil
	case bytes.HasPrefix(data, []byte("PK\x03\x04")):
		if ext != ".docx" {
			return "", errors.New("only .docx Word documents are supported")
		}
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return "", errors.New("the Word document is corrupted")
		}
		for _, f := range zr.File {
			if f.Name == "word/document.xml" {
				return MimeDOCX, nil
			}
		}
		return "", errors.New("the file is not a Word document")
	}
	return "", errors.New("only PDF and DOCX files are supported")
}

// SanitizeFilename keeps a display-safe base name.
func SanitizeFilename(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '"' || r == '/' {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." {
		name = "document"
	}
	if len(name) > 200 {
		ext := filepath.Ext(name)
		name = name[:200-len(ext)] + ext
	}
	return name
}

var validKinds = map[string]bool{"CV": true, "COVER_LETTER": true, "EVIDENCE": true}

func (s *Service) Upload(ctx context.Context, userID uuid.UUID, kind, filename string, data []byte, ip string) (Document, error) {
	if s.store == nil {
		return Document{}, errStorageUnavailable
	}
	if !validKinds[kind] {
		return Document{}, httpx.Validation(map[string]string{"kind": "Choose CV, COVER_LETTER or EVIDENCE."})
	}
	if len(data) == 0 {
		return Document{}, httpx.Validation(map[string]string{"file": "The file is empty."})
	}
	if int64(len(data)) > s.maxSize {
		return Document{}, httpx.NewError(httpx.CodePayloadTooLarge, fmt.Sprintf("Files must be at most %d MB.", s.maxSize>>20))
	}
	filename = SanitizeFilename(filename)
	mime, err := DetectType(data, filename)
	if err != nil {
		return Document{}, httpx.Validation(map[string]string{"file": err.Error()})
	}
	sum := sha256.Sum256(data)
	doc := Document{ID: ids.New(), Kind: kind, OriginalFilename: filename, MimeType: mime, SizeBytes: int64(len(data)),
		CreatedAt: time.Now(), storageKey: "documents/" + ids.New().String()}

	// Store the object first; if the DB insert fails, remove it so no orphan remains.
	if err := s.store.Put(ctx, doc.storageKey, data, mime); err != nil {
		return Document{}, fmt.Errorf("store document: %w", err)
	}
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO profile_documents (id, user_id, kind, original_filename, mime_type, size_bytes, sha256, storage_key)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, doc.ID, userID, kind, filename, mime, doc.SizeBytes, sum[:], doc.storageKey); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "document.upload", SubjectType: "document", SubjectID: &doc.ID,
			Outcome: audit.Success, IP: ip, Metadata: map[string]any{"kind": kind, "size_bytes": doc.SizeBytes, "mime_type": mime}})
	})
	if err != nil {
		if delErr := s.store.Delete(context.WithoutCancel(ctx), doc.storageKey); delErr != nil {
			s.log.Error("orphaned object after failed upload", "key", doc.storageKey, "err", delErr)
		}
		return Document{}, err
	}
	return doc, nil
}

func scanDoc(row pgx.Row) (Document, error) {
	var d Document
	err := row.Scan(&d.ID, &d.Kind, &d.OriginalFilename, &d.MimeType, &d.SizeBytes, &d.CreatedAt, &d.storageKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return Document{}, errDocNotFound
	}
	return d, err
}

const docCols = `id, kind, original_filename, mime_type, size_bytes, created_at, storage_key`

func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]Document, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+docCols+` FROM profile_documents WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Document{}
	for rows.Next() {
		d, err := scanDoc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Service) get(ctx context.Context, q db.Querier, userID, id uuid.UUID) (Document, error) {
	return scanDoc(q.QueryRow(ctx, `SELECT `+docCols+` FROM profile_documents WHERE id = $1 AND user_id = $2`, id, userID))
}

func (s *Service) Download(ctx context.Context, userID, id uuid.UUID, ip string) (Document, []byte, error) {
	if s.store == nil {
		return Document{}, nil, errStorageUnavailable
	}
	d, err := s.get(ctx, s.pool, userID, id)
	if err != nil {
		return Document{}, nil, err
	}
	data, err := s.store.Get(ctx, d.storageKey)
	if err != nil {
		return Document{}, nil, fmt.Errorf("read document: %w", err)
	}
	_ = audit.Record(ctx, s.pool, audit.Event{UserID: &userID, Action: "document.download", SubjectType: "document", SubjectID: &id,
		Outcome: audit.Success, IP: ip})
	return d, data, nil
}

func (s *Service) Delete(ctx context.Context, userID, id uuid.UUID, ip string) error {
	var key string
	err := db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		d, err := s.get(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		key = d.storageKey
		if _, err := tx.Exec(ctx, `DELETE FROM profile_documents WHERE id = $1 AND user_id = $2`, id, userID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "document.delete", SubjectType: "document", SubjectID: &id,
			Outcome: audit.Success, IP: ip})
	})
	if err != nil {
		return err
	}
	if s.store != nil {
		if err := s.store.Delete(ctx, key); err != nil {
			// The record is gone, so the object is unreachable; log for cleanup.
			s.log.Error("failed to delete stored object", "key", key, "err", err)
		}
	}
	return nil
}

// ---- Extraction ----

const extCols = `id, document_id, status, proposal, error_code, error_message, provider, model, prompt_version, created_at, updated_at`

func scanExtraction(row pgx.Row) (Extraction, error) {
	var e Extraction
	var proposal []byte
	err := row.Scan(&e.ID, &e.DocumentID, &e.Status, &proposal, &e.ErrorCode, &e.ErrorMessage, &e.Provider, &e.Model, &e.PromptVersion, &e.CreatedAt, &e.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Extraction{}, errExtractionNotFound
	}
	if proposal != nil {
		e.Proposal = proposal
	}
	return e, err
}

// StartExtraction queues a CV extraction job in the same transaction as the
// extraction record. It fails fast when the AI provider is not configured.
func (s *Service) StartExtraction(ctx context.Context, userID, docID uuid.UUID, ip string) (Extraction, error) {
	if s.store == nil {
		return Extraction{}, errStorageUnavailable
	}
	if s.ai == nil {
		return Extraction{}, httpx.NewError(httpx.CodeUnavailable, "The AI service is not configured on this server.")
	}
	infoCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	info, err := s.ai.Info(infoCtx)
	if err != nil {
		return Extraction{}, &httpx.Error{Code: httpx.CodeUnavailable, Message: "The AI service is unreachable. Try again shortly.", Cause: err}
	}
	if !info.LLMConfigured {
		return Extraction{}, &httpx.Error{Code: httpx.CodeUnavailable,
			Message: "No AI provider is configured, so CVs cannot be analysed automatically. You can still enter your profile manually.",
			Details: map[string]string{"reason": "AI_PROVIDER_UNAVAILABLE"}}
	}

	var ext Extraction
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		d, err := s.get(ctx, tx, userID, docID)
		if err != nil {
			return err
		}
		if d.Kind != "CV" {
			return httpx.Validation(map[string]string{"document": "Only CV documents can be analysed."})
		}
		id := ids.New()
		ext, err = scanExtraction(tx.QueryRow(ctx, `INSERT INTO profile_extractions (id, user_id, document_id, status)
			VALUES ($1, $2, $3, 'QUEUED') RETURNING `+extCols, id, userID, docID))
		if err != nil {
			return err
		}
		if _, err := s.jobs.InsertTx(ctx, tx, ExtractCVArgs{ExtractionID: id, UserID: userID}, nil); err != nil {
			return fmt.Errorf("enqueue extraction: %w", err)
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "profile.extraction.start", SubjectType: "extraction",
			SubjectID: &id, Outcome: audit.Success, IP: ip, Metadata: map[string]any{"document_id": docID}})
	})
	return ext, err
}

func (s *Service) GetExtraction(ctx context.Context, userID, id uuid.UUID) (Extraction, error) {
	return scanExtraction(s.pool.QueryRow(ctx, `SELECT `+extCols+` FROM profile_extractions WHERE id = $1 AND user_id = $2`, id, userID))
}

// LatestExtraction returns the most recent extraction for a document.
func (s *Service) LatestExtraction(ctx context.Context, userID, docID uuid.UUID) (Extraction, error) {
	return scanExtraction(s.pool.QueryRow(ctx, `SELECT `+extCols+` FROM profile_extractions
		WHERE document_id = $1 AND user_id = $2 ORDER BY created_at DESC LIMIT 1`, docID, userID))
}

// ApplyExtraction writes the user-reviewed items to the profile and closes the
// extraction. Only PENDING_REVIEW extractions can be applied (exactly once).
func (s *Service) ApplyExtraction(ctx context.Context, userID, id uuid.UUID, batch profiles.ImportBatch, ip string) (profiles.ImportResult, error) {
	var res profiles.ImportResult
	err := db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var status string
		err := tx.QueryRow(ctx, `SELECT status FROM profile_extractions WHERE id = $1 AND user_id = $2 FOR UPDATE`, id, userID).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return errExtractionNotFound
		}
		if err != nil {
			return err
		}
		if status != "PENDING_REVIEW" {
			return httpx.NewError(httpx.CodeInvalidTransition, "This extraction is not awaiting review.")
		}
		if res, err = profiles.Import(ctx, tx, userID, batch); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE profile_extractions SET status = 'APPLIED', applied_at = now(), updated_at = now() WHERE id = $1`, id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "profile.extraction.apply", SubjectType: "extraction",
			SubjectID: &id, Outcome: audit.Success, IP: ip,
			Metadata: map[string]any{"created": res.Created, "verified": res.Verified, "skipped_skills": res.SkippedSkills}})
	})
	return res, err
}

func (s *Service) DiscardExtraction(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `UPDATE profile_extractions SET status = 'DISCARDED', updated_at = now()
		WHERE id = $1 AND user_id = $2 AND status = 'PENDING_REVIEW'`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		if _, err := s.GetExtraction(ctx, userID, id); err != nil {
			return err
		}
		return httpx.NewError(httpx.CodeInvalidTransition, "This extraction is not awaiting review.")
	}
	return nil
}
