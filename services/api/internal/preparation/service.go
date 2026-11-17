package preparation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
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

	"github.com/careerpilot/careerpilot/services/api/internal/aiclient"
	"github.com/careerpilot/careerpilot/services/api/internal/audit"
	"github.com/careerpilot/careerpilot/services/api/internal/jobs"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
	"github.com/careerpilot/careerpilot/services/api/internal/profiles"
)

type Service struct {
	pool  *pgxpool.Pool
	ai    *aiclient.Client // nil when the AI service is not configured
	prof  *profiles.Service
	queue *jobs.Client
	log   *slog.Logger
	now   func() time.Time
}

func NewService(pool *pgxpool.Pool, ai *aiclient.Client, prof *profiles.Service, queue *jobs.Client, log *slog.Logger) *Service {
	return &Service{pool: pool, ai: ai, prof: prof, queue: queue, log: log, now: time.Now}
}

var (
	errDocNotFound     = httpx.NewError(httpx.CodeNotFound, "Document not found.")
	errVersionNotFound = httpx.NewError(httpx.CodeNotFound, "Version not found.")
	errJobNotFound     = httpx.NewError(httpx.CodeNotFound, "Job not found.")
	errAINotConfigured = httpx.NewError(httpx.CodeUnavailable, "The AI service is not configured on this server.")
)

// Codes that can never be approved, even with acknowledgement: the content is
// incomplete rather than possibly unsupported.
var blockingCodes = map[string]bool{"NEEDS_INPUT": true, "EMPTY": true, "PLACEHOLDER": true}

// loadSheet builds the user's current fact sheet. notes come from the document.
func (s *Service) loadSheet(ctx context.Context, userID uuid.UUID, notes string) (sheet, error) {
	p, err := s.prof.Get(ctx, userID)
	if err != nil {
		return sheet{}, err
	}
	var name, email string
	if err := s.pool.QueryRow(ctx, `SELECT display_name, email FROM users WHERE id = $1`, userID).Scan(&name, &email); err != nil {
		return sheet{}, err
	}
	return buildSheet(p, name, email, notes, s.now()), nil
}

// validate runs the AI service's deterministic fact check. It returns nil
// (not validated) when the service is unavailable, so edits are never lost.
func (s *Service) validate(ctx context.Context, c Content, sh sheet) *Validation {
	if s.ai == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rep, err := s.ai.ValidateDocument(ctx, aiclient.ValidateDocumentRequest{Content: c.raw(), Facts: sh.Facts, Candidate: sh.Candidate})
	if err != nil {
		s.log.Warn("document validation unavailable", "err", err)
		return nil
	}
	if rep.Issues == nil {
		rep.Issues = []aiclient.Issue{}
	}
	return &rep
}

func cleanQuestions(in []string) ([]string, error) {
	out := []string{}
	for _, q := range in {
		if q = strings.TrimSpace(q); q != "" {
			if utf8.RuneCountInString(q) > maxQuestion {
				return nil, httpx.Validation(map[string]string{"questions": fmt.Sprintf("Each question must be at most %d characters.", maxQuestion)})
			}
			out = append(out, q)
		}
	}
	if len(out) > maxQuestions {
		return nil, httpx.Validation(map[string]string{"questions": fmt.Sprintf("At most %d questions.", maxQuestions)})
	}
	return out, nil
}

// GenerateInput starts a document. Source AI (default) queues an LLM draft;
// PROFILE builds one immediately from verified facts, without AI.
type GenerateInput struct {
	Kind      string   `json:"kind"`
	Source    string   `json:"source"`
	Notes     string   `json:"notes"`
	Questions []string `json:"questions"`
}

const (
	SourceAI      = "AI"
	SourceProfile = "PROFILE"
)

// normalize validates the input and returns the cleaned questions.
func (in *GenerateInput) normalize() ([]string, error) {
	if !validKinds[in.Kind] {
		return nil, httpx.Validation(map[string]string{"kind": "Choose CV, COVER_LETTER or APPLICATION_ANSWERS."})
	}
	if in.Source == "" {
		in.Source = SourceAI
	}
	if in.Source != SourceAI && in.Source != SourceProfile {
		return nil, httpx.Validation(map[string]string{"source": "Choose AI or PROFILE."})
	}
	in.Notes = strings.TrimSpace(in.Notes)
	if utf8.RuneCountInString(in.Notes) > maxNotesChars {
		return nil, httpx.Validation(map[string]string{"notes": fmt.Sprintf("Use at most %d characters.", maxNotesChars)})
	}
	qs, err := cleanQuestions(in.Questions)
	if err != nil {
		return nil, err
	}
	if in.Kind == KindAnswers && len(qs) == 0 {
		return nil, httpx.Validation(map[string]string{"questions": "Add at least one application question."})
	}
	if in.Kind != KindAnswers {
		qs = []string{}
	}
	return qs, nil
}

// upsertDoc creates or reuses the (user, job, kind) document and stores the
// generation inputs on it.
func upsertDoc(ctx context.Context, tx pgx.Tx, userID, jobID uuid.UUID, in GenerateInput, qs []string) (uuid.UUID, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM jobs WHERE id = $1)`, jobID).Scan(&exists); err != nil {
		return uuid.Nil, err
	}
	if !exists {
		return uuid.Nil, errJobNotFound
	}
	rawQs, _ := json.Marshal(qs)
	var docID uuid.UUID
	err := tx.QueryRow(ctx, `INSERT INTO application_documents (id, user_id, job_id, kind, notes, questions)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, job_id, kind) DO UPDATE SET notes = EXCLUDED.notes, questions = EXCLUDED.questions, updated_at = now()
		RETURNING id`, ids.New(), userID, jobID, in.Kind, in.Notes, rawQs).Scan(&docID)
	return docID, err
}

// CreateFromProfile builds a document deterministically from verified facts
// (no AI) as a new TEMPLATE version, so documents can be prepared even when
// no LLM provider is configured.
func (s *Service) CreateFromProfile(ctx context.Context, userID, jobID uuid.UUID, in GenerateInput, ip string) (Document, error) {
	qs, err := in.normalize()
	if err != nil {
		return Document{}, err
	}
	sh, err := s.loadSheet(ctx, userID, in.Notes)
	if err != nil {
		return Document{}, err
	}
	if in.Kind == KindCV && countUsable(sh) == 0 {
		return Document{}, &httpx.Error{Code: httpx.CodeValidationFailed,
			Message: "Your profile has no verified experience, skills or education to build a CV from. Add or verify facts on your profile first.",
			Details: map[string]string{"profile": "PROFILE_INCOMPLETE"}}
	}
	c, val, err := s.prepareContent(ctx, fromProfile(in.Kind, sh, qs), in.Kind, sh)
	if err != nil {
		return Document{}, err
	}
	var doc Document
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		docID, err := upsertDoc(ctx, tx, userID, jobID, in, qs)
		if err != nil {
			return err
		}
		vid, _, err := insertVersion(ctx, tx, userID, docID, newVersion{Origin: OriginTemplate, Content: c, Validation: val, FactsHash: sh.Hash})
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "application_document.create_from_profile",
			SubjectType: "application_document", SubjectID: &docID, Outcome: audit.Success, IP: ip,
			Metadata: map[string]any{"kind": in.Kind, "job_id": jobID, "version_id": vid}}); err != nil {
			return err
		}
		doc, err = getDoc(ctx, tx, userID, docID)
		return err
	})
	return doc, err
}

// RequestGeneration creates (or reuses) the user's document of this kind for
// the job and queues a draft generation in the same transaction.
func (s *Service) RequestGeneration(ctx context.Context, userID, jobID uuid.UUID, in GenerateInput, ip string) (Document, Generation, error) {
	qs, err := in.normalize()
	if err != nil {
		return Document{}, Generation{}, err
	}
	if err := s.requireLLM(ctx); err != nil {
		return Document{}, Generation{}, err
	}

	var doc Document
	var gen Generation
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		docID, err := upsertDoc(ctx, tx, userID, jobID, in, qs)
		if err != nil {
			return err
		}
		// A generation abandoned by a crashed worker must not block new drafts.
		if _, err := tx.Exec(ctx, `UPDATE application_document_generations SET status = 'FAILED', error_code = 'ABANDONED',
			error_message = 'The previous attempt did not finish.', updated_at = now()
			WHERE document_id = $1 AND status IN ('QUEUED', 'RUNNING') AND updated_at < now() - interval '15 minutes'`, docID); err != nil {
			return err
		}
		genID := ids.New()
		_, err = tx.Exec(ctx, `INSERT INTO application_document_generations (id, document_id, user_id, status) VALUES ($1, $2, $3, 'QUEUED')`,
			genID, docID, userID)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return httpx.NewError(httpx.CodeConflict, "A draft is already being generated for this document.")
		}
		if err != nil {
			return err
		}
		if _, err := s.queue.InsertTx(ctx, tx, GenerateDocumentArgs{GenerationID: genID, UserID: userID}, nil); err != nil {
			return fmt.Errorf("enqueue generation: %w", err)
		}
		if err := audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "application_document.generate.request",
			SubjectType: "application_document", SubjectID: &docID, Outcome: audit.Success, IP: ip,
			Metadata: map[string]any{"kind": in.Kind, "job_id": jobID, "questions": len(qs), "has_notes": in.Notes != ""}}); err != nil {
			return err
		}
		if doc, err = getDoc(ctx, tx, userID, docID); err != nil {
			return err
		}
		gen, err = getGeneration(ctx, tx, genID)
		return err
	})
	return doc, gen, err
}

func (s *Service) requireLLM(ctx context.Context) error {
	if s.ai == nil {
		return errAINotConfigured
	}
	ictx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	info, err := s.ai.Info(ictx)
	if err != nil {
		return &httpx.Error{Code: httpx.CodeUnavailable, Message: "The AI service is unreachable. Try again shortly.", Cause: err}
	}
	if !info.LLMConfigured || !info.Has("documents.generate") {
		return httpx.NewError(httpx.CodeAIUnavailable,
			"No AI provider is configured, so drafts cannot be generated. You can start from your profile instead and tailor the document yourself.")
	}
	return nil
}

const docCols = `d.id, d.job_id, d.kind, d.notes, d.questions, d.current_version_id, d.approved_version_id, d.approved_at,
	j.title, j.company_name, d.created_at, d.updated_at`

func scanDoc(row pgx.Row) (Document, error) {
	var d Document
	var qs []byte
	err := row.Scan(&d.ID, &d.JobID, &d.Kind, &d.Notes, &qs, &d.CurrentVersionID, &d.ApprovedVersionID, &d.ApprovedAt,
		&d.JobTitle, &d.Company, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Document{}, errDocNotFound
	}
	if err != nil {
		return Document{}, err
	}
	if err := json.Unmarshal(qs, &d.Questions); err != nil || d.Questions == nil {
		d.Questions = []string{}
	}
	d.derive()
	return d, nil
}

func getDoc(ctx context.Context, q db.Querier, userID, id uuid.UUID) (Document, error) {
	return scanDoc(q.QueryRow(ctx, `SELECT `+docCols+` FROM application_documents d JOIN jobs j ON j.id = d.job_id
		WHERE d.id = $1 AND d.user_id = $2`, id, userID))
}

// ListDocuments returns the user's documents, optionally for one job.
func (s *Service) ListDocuments(ctx context.Context, userID uuid.UUID, jobID *uuid.UUID) ([]Document, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+docCols+` FROM application_documents d JOIN jobs j ON j.id = d.job_id
		WHERE d.user_id = $1 AND ($2::uuid IS NULL OR d.job_id = $2) ORDER BY d.updated_at DESC LIMIT 200`, userID, jobID)
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

const genCols = `id, status, version_id, error_code, error_message, created_at, updated_at`

func scanGeneration(row pgx.Row) (Generation, error) {
	var g Generation
	err := row.Scan(&g.ID, &g.Status, &g.VersionID, &g.ErrorCode, &g.ErrorMessage, &g.CreatedAt, &g.UpdatedAt)
	return g, err
}

func getGeneration(ctx context.Context, q db.Querier, id uuid.UUID) (Generation, error) {
	return scanGeneration(q.QueryRow(ctx, `SELECT `+genCols+` FROM application_document_generations WHERE id = $1`, id))
}

// DocumentView is a document with its current version, latest generation and
// the user's current citable facts (to show the evidence behind each block).
type DocumentView struct {
	Document
	Current    *Version    `json:"current_version"`
	Generation *Generation `json:"latest_generation"`
	Facts      []FactRef   `json:"facts"`
}

type FactRef struct {
	Ref  string `json:"ref"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

func factRefs(sh sheet) []FactRef {
	out := make([]FactRef, 0, len(sh.Facts))
	for _, f := range sh.Facts {
		text := f.Text
		if r := []rune(text); len(r) > 300 {
			text = string(r[:300]) + "…"
		}
		out = append(out, FactRef{Ref: f.Ref, Kind: f.Kind, Text: text})
	}
	return out
}

func (s *Service) Get(ctx context.Context, userID, docID uuid.UUID) (DocumentView, error) {
	// Read the generation BEFORE the document: a generation commits together
	// with its version, so a SUCCEEDED generation guarantees the document
	// read that follows sees that version (the reverse order could report
	// SUCCEEDED with no content, and the UI would stop polling).
	var gen *Generation
	g, err := scanGeneration(s.pool.QueryRow(ctx, `SELECT `+genCols+` FROM application_document_generations
		WHERE document_id = $1 AND user_id = $2 ORDER BY created_at DESC LIMIT 1`, docID, userID))
	if err == nil {
		gen = &g
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return DocumentView{}, err
	}
	d, err := getDoc(ctx, s.pool, userID, docID)
	if err != nil {
		return DocumentView{}, err
	}
	sh, err := s.loadSheet(ctx, userID, d.Notes)
	if err != nil {
		return DocumentView{}, err
	}
	v := DocumentView{Document: d, Facts: factRefs(sh), Generation: gen}
	if d.CurrentVersionID != nil {
		cur, err := s.GetVersion(ctx, userID, docID, *d.CurrentVersionID)
		if err != nil {
			return DocumentView{}, err
		}
		v.Current = &cur
	}
	return v, nil
}

const versionMetaCols = `id, version_no, origin, restored_from, validation_status, prompt_version, model, created_at`

func (s *Service) ListVersions(ctx context.Context, userID, docID uuid.UUID) ([]VersionMeta, error) {
	if _, err := getDoc(ctx, s.pool, userID, docID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+versionMetaCols+` FROM application_document_versions
		WHERE document_id = $1 AND user_id = $2 ORDER BY version_no DESC`, docID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VersionMeta{}
	for rows.Next() {
		var m VersionMeta
		if err := rows.Scan(&m.ID, &m.VersionNo, &m.Origin, &m.RestoredFrom, &m.ValidationStatus, &m.PromptVersion, &m.Model, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Service) GetVersion(ctx context.Context, userID, docID, versionID uuid.UUID) (Version, error) {
	v, factsHash, notes, err := loadVersion(ctx, s.pool, userID, docID, versionID)
	if err != nil {
		return Version{}, err
	}
	sh, err := s.loadSheet(ctx, userID, notes)
	if err != nil {
		return Version{}, err
	}
	v.FactsChanged = !bytes.Equal(factsHash, sh.Hash)
	return v, nil
}

func loadVersion(ctx context.Context, q db.Querier, userID, docID, versionID uuid.UUID) (Version, []byte, string, error) {
	var v Version
	var content, validation, qs, ws, factsHash []byte
	var notes string
	err := q.QueryRow(ctx, `SELECT v.id, v.version_no, v.origin, v.restored_from, v.validation_status, v.prompt_version, v.model,
			v.created_at, v.content, v.validation, v.questions, v.warnings, v.facts_hash, d.notes
		FROM application_document_versions v JOIN application_documents d ON d.id = v.document_id
		WHERE v.id = $1 AND v.document_id = $2 AND v.user_id = $3`, versionID, docID, userID).Scan(
		&v.ID, &v.VersionNo, &v.Origin, &v.RestoredFrom, &v.ValidationStatus, &v.PromptVersion, &v.Model, &v.CreatedAt,
		&content, &validation, &qs, &ws, &factsHash, &notes)
	if errors.Is(err, pgx.ErrNoRows) {
		return Version{}, nil, "", errVersionNotFound
	}
	if err != nil {
		return Version{}, nil, "", err
	}
	if err := json.Unmarshal(content, &v.Content); err != nil {
		return Version{}, nil, "", fmt.Errorf("decode version content: %w", err)
	}
	if validation != nil {
		v.Validation = &Validation{}
		if err := json.Unmarshal(validation, v.Validation); err != nil {
			return Version{}, nil, "", fmt.Errorf("decode validation: %w", err)
		}
	}
	_ = json.Unmarshal(qs, &v.Questions)
	_ = json.Unmarshal(ws, &v.Warnings)
	if v.Questions == nil {
		v.Questions = []string{}
	}
	if v.Warnings == nil {
		v.Warnings = []string{}
	}
	return v, factsHash, notes, nil
}

type newVersion struct {
	Origin       string
	RestoredFrom *uuid.UUID
	Content      Content
	Validation   *Validation
	Questions    []string
	Warnings     []string
	Usage        *aiclient.AnalyzeUsage
	FactsHash    []byte
	// BaseVersionID, when set, must equal the document's current version
	// (optimistic concurrency for edits).
	BaseVersionID *uuid.UUID
}

// insertVersion appends a version and makes it current. It locks the document
// row so version numbers are gap-free and concurrent edits are detected.
// When the content is identical to the current version it returns that
// version instead (saving twice is idempotent).
func insertVersion(ctx context.Context, tx pgx.Tx, userID, docID uuid.UUID, nv newVersion) (uuid.UUID, bool, error) {
	var current *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT current_version_id FROM application_documents WHERE id = $1 AND user_id = $2 FOR UPDATE`,
		docID, userID).Scan(&current); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, errDocNotFound
		}
		return uuid.Nil, false, err
	}
	if nv.BaseVersionID != nil && (current == nil || *current != *nv.BaseVersionID) {
		return uuid.Nil, false, httpx.NewError(httpx.CodeConflict, "This document changed since you opened it. Reload to see the latest version.")
	}
	raw := nv.Content.raw()
	sum := sha256.Sum256(raw)
	if current != nil && nv.Origin == OriginEdit {
		var curHash []byte
		if err := tx.QueryRow(ctx, `SELECT content_hash FROM application_document_versions WHERE id = $1`, *current).Scan(&curHash); err != nil {
			return uuid.Nil, false, err
		}
		if bytes.Equal(curHash, sum[:]) {
			return *current, false, nil
		}
	}
	status := NotValidated
	var validation []byte
	if nv.Validation != nil {
		status = nv.Validation.Status
		validation, _ = json.Marshal(nv.Validation)
	}
	qs, _ := json.Marshal(nonNil(nv.Questions))
	ws, _ := json.Marshal(nonNil(nv.Warnings))
	var provider, model, prompt *string
	var in, out *int
	if u := nv.Usage; u != nil {
		provider, model, prompt, in, out = &u.Provider, &u.Model, &u.PromptVersion, &u.InputTokens, &u.OutputTokens
	}
	id := ids.New()
	if _, err := tx.Exec(ctx, `INSERT INTO application_document_versions (id, document_id, user_id, version_no, origin, restored_from,
			content, content_hash, facts_hash, validation, validation_status, questions, warnings, provider, model, prompt_version,
			input_tokens, output_tokens)
		VALUES ($1, $2, $3, (SELECT coalesce(max(version_no), 0) + 1 FROM application_document_versions WHERE document_id = $2),
			$4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`,
		id, docID, userID, nv.Origin, nv.RestoredFrom, raw, sum[:], nv.FactsHash, validation, status, qs, ws,
		provider, model, prompt, in, out); err != nil {
		return uuid.Nil, false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE application_documents SET current_version_id = $2, updated_at = now() WHERE id = $1`, docID, id); err != nil {
		return uuid.Nil, false, err
	}
	return id, true, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// prepareContent sanitises content, renders structural text from the
// profile and validates it against the current facts.
func (s *Service) prepareContent(ctx context.Context, c Content, kind string, sh sheet) (Content, *Validation, error) {
	if err := c.sanitize(kind); err != nil {
		return Content{}, nil, err
	}
	sh.render(&c)
	return c, s.validate(ctx, c, sh), nil
}

// SaveEdit stores the user's edited content as a new version.
func (s *Service) SaveEdit(ctx context.Context, userID, docID, baseVersionID uuid.UUID, c Content, ip string) (Version, error) {
	d, err := getDoc(ctx, s.pool, userID, docID)
	if err != nil {
		return Version{}, err
	}
	sh, err := s.loadSheet(ctx, userID, d.Notes)
	if err != nil {
		return Version{}, err
	}
	c, val, err := s.prepareContent(ctx, c, d.Kind, sh)
	if err != nil {
		return Version{}, err
	}
	var id uuid.UUID
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var created bool
		id, created, err = insertVersion(ctx, tx, userID, docID, newVersion{Origin: OriginEdit, Content: c, Validation: val,
			FactsHash: sh.Hash, BaseVersionID: &baseVersionID})
		if err != nil || !created {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "application_document.edit", SubjectType: "application_document",
			SubjectID: &docID, Outcome: audit.Success, IP: ip, Metadata: map[string]any{"version_id": id, "validation": statusOf(val)}})
	})
	if err != nil {
		return Version{}, err
	}
	return s.GetVersion(ctx, userID, docID, id)
}

func statusOf(v *Validation) string {
	if v == nil {
		return NotValidated
	}
	return v.Status
}

// Restore copies an earlier version into a new current version.
func (s *Service) Restore(ctx context.Context, userID, docID, versionID uuid.UUID, ip string) (Version, error) {
	d, err := getDoc(ctx, s.pool, userID, docID)
	if err != nil {
		return Version{}, err
	}
	old, _, _, err := loadVersion(ctx, s.pool, userID, docID, versionID)
	if err != nil {
		return Version{}, err
	}
	sh, err := s.loadSheet(ctx, userID, d.Notes)
	if err != nil {
		return Version{}, err
	}
	c, val, err := s.prepareContent(ctx, old.Content, d.Kind, sh)
	if err != nil {
		return Version{}, err
	}
	var id uuid.UUID
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		id, _, err = insertVersion(ctx, tx, userID, docID, newVersion{Origin: OriginRestored, RestoredFrom: &versionID, Content: c,
			Validation: val, Questions: old.Questions, FactsHash: sh.Hash})
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "application_document.restore", SubjectType: "application_document",
			SubjectID: &docID, Outcome: audit.Success, IP: ip, Metadata: map[string]any{"from_version": old.VersionNo, "version_id": id}})
	})
	if err != nil {
		return Version{}, err
	}
	return s.GetVersion(ctx, userID, docID, id)
}

// Recheck validates the current version against the CURRENT profile without
// creating a version (the stored validation reflects the profile at save time).
func (s *Service) Recheck(ctx context.Context, userID, docID uuid.UUID) (*Validation, error) {
	d, err := getDoc(ctx, s.pool, userID, docID)
	if err != nil {
		return nil, err
	}
	if d.CurrentVersionID == nil {
		return nil, httpx.NewError(httpx.CodeInvalidTransition, "This document has no content yet.")
	}
	v, _, _, err := loadVersion(ctx, s.pool, userID, docID, *d.CurrentVersionID)
	if err != nil {
		return nil, err
	}
	sh, err := s.loadSheet(ctx, userID, d.Notes)
	if err != nil {
		return nil, err
	}
	val := s.validate(ctx, v.Content, sh)
	if val == nil {
		return nil, httpx.NewError(httpx.CodeUnavailable, "The fact check is unavailable because the AI service cannot be reached.")
	}
	return val, nil
}

type ApproveInput struct {
	VersionID         uuid.UUID `json:"version_id"`
	AcknowledgeIssues bool      `json:"acknowledge_issues"`
}

// Approve records the user's approval of the current version after a fresh
// fact check against the current profile.
func (s *Service) Approve(ctx context.Context, userID, docID uuid.UUID, in ApproveInput, ip string) (Document, *Validation, error) {
	d, err := getDoc(ctx, s.pool, userID, docID)
	if err != nil {
		return Document{}, nil, err
	}
	if d.CurrentVersionID == nil || *d.CurrentVersionID != in.VersionID {
		return Document{}, nil, httpx.NewError(httpx.CodeConflict, "Only the current version can be approved. Reload to see the latest.")
	}
	v, _, _, err := loadVersion(ctx, s.pool, userID, docID, in.VersionID)
	if err != nil {
		return Document{}, nil, err
	}
	sh, err := s.loadSheet(ctx, userID, d.Notes)
	if err != nil {
		return Document{}, nil, err
	}
	val := s.validate(ctx, v.Content, sh)
	if val == nil {
		return Document{}, nil, httpx.NewError(httpx.CodeUnavailable,
			"Approval needs a fact check, and the AI service cannot be reached. Try again shortly.")
	}
	var blocking, errorsN int
	acknowledged := []string{}
	for _, is := range val.Issues {
		if is.Severity != "ERROR" {
			continue
		}
		errorsN++
		if blockingCodes[is.Code] {
			blocking++
		} else {
			acknowledged = append(acknowledged, is.Code+":"+is.BlockID)
		}
	}
	if blocking > 0 {
		return Document{}, val, &httpx.Error{Code: httpx.CodeValidationFailed,
			Message: fmt.Sprintf("%d part(s) of this document are incomplete. Fill in or remove them before approving.", blocking),
			Details: map[string]string{"validation": "INCOMPLETE"}}
	}
	if errorsN > 0 && !in.AcknowledgeIssues {
		return Document{}, val, &httpx.Error{Code: httpx.CodeValidationFailed,
			Message: fmt.Sprintf("The fact check found %d statement(s) your profile does not support. Fix them, or confirm that you have checked them.", errorsN),
			Details: map[string]string{"acknowledge_issues": "Required to approve with unresolved fact-check errors."}}
	}
	if errorsN == 0 {
		acknowledged = []string{}
	}
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var current *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT current_version_id FROM application_documents WHERE id = $1 AND user_id = $2 FOR UPDATE`,
			docID, userID).Scan(&current); err != nil {
			return err
		}
		if current == nil || *current != in.VersionID {
			return httpx.NewError(httpx.CodeConflict, "The document changed while approving. Reload and try again.")
		}
		rawVal, _ := json.Marshal(val)
		rawAck, _ := json.Marshal(acknowledged)
		if _, err := tx.Exec(ctx, `UPDATE application_document_approvals SET revoked_at = now() WHERE document_id = $1 AND revoked_at IS NULL`, docID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO application_document_approvals (id, document_id, version_id, user_id, validation, acknowledged_issues)
			VALUES ($1, $2, $3, $4, $5, $6)`, ids.New(), docID, in.VersionID, userID, rawVal, rawAck); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE application_documents SET approved_version_id = $2, approved_at = now(), updated_at = now() WHERE id = $1`,
			docID, in.VersionID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "application_document.approve", SubjectType: "application_document",
			SubjectID: &docID, Outcome: audit.Success, IP: ip, Metadata: map[string]any{"version_no": v.VersionNo,
				"validation": val.Status, "acknowledged_issues": len(acknowledged)}})
	})
	if err != nil {
		return Document{}, nil, err
	}
	d, err = getDoc(ctx, s.pool, userID, docID)
	return d, val, err
}

func (s *Service) RevokeApproval(ctx context.Context, userID, docID uuid.UUID, ip string) (Document, error) {
	err := db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE application_documents SET approved_version_id = NULL, approved_at = NULL, updated_at = now()
			WHERE id = $1 AND user_id = $2 AND approved_version_id IS NOT NULL`, docID, userID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			if _, err := getDoc(ctx, tx, userID, docID); err != nil {
				return err
			}
			return httpx.NewError(httpx.CodeInvalidTransition, "This document is not approved.")
		}
		if _, err := tx.Exec(ctx, `UPDATE application_document_approvals SET revoked_at = now() WHERE document_id = $1 AND revoked_at IS NULL`, docID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "application_document.approval.revoke",
			SubjectType: "application_document", SubjectID: &docID, Outcome: audit.Success, IP: ip})
	})
	if err != nil {
		return Document{}, err
	}
	return getDoc(ctx, s.pool, userID, docID)
}

func (s *Service) Delete(ctx context.Context, userID, docID uuid.UUID, ip string) error {
	return db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM application_documents WHERE id = $1 AND user_id = $2`, docID, userID)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return httpx.NewError(httpx.CodeConflict,
				"This document is part of an approved application. Reopen or withdraw the application before deleting it.")
		}
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errDocNotFound
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "application_document.delete",
			SubjectType: "application_document", SubjectID: &docID, Outcome: audit.Success, IP: ip})
	})
}

// Export renders a version for download and records the export.
func (s *Service) Export(ctx context.Context, userID, docID, versionID uuid.UUID, format, ip string) ([]byte, string, string, error) {
	if _, ok := formatMIME[format]; !ok {
		return nil, "", "", httpx.Validation(map[string]string{"format": "Choose docx, txt or md."})
	}
	d, err := getDoc(ctx, s.pool, userID, docID)
	if err != nil {
		return nil, "", "", err
	}
	v, _, _, err := loadVersion(ctx, s.pool, userID, docID, versionID)
	if err != nil {
		return nil, "", "", err
	}
	data, mime, name, err := render(v.Content, format, d.Company, v.VersionNo)
	if err != nil {
		return nil, "", "", err
	}
	_ = audit.Record(ctx, s.pool, audit.Event{UserID: &userID, Action: "application_document.export", SubjectType: "application_document",
		SubjectID: &docID, Outcome: audit.Success, IP: ip, Metadata: map[string]any{"format": format, "version_no": v.VersionNo,
			"approved": d.ApprovedVersionID != nil && *d.ApprovedVersionID == versionID}})
	return data, mime, name, nil
}
