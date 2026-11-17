package outreach

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/careerpilot/careerpilot/services/api/internal/aiclient"
	"github.com/careerpilot/careerpilot/services/api/internal/audit"
	"github.com/careerpilot/careerpilot/services/api/internal/automation"
	"github.com/careerpilot/careerpilot/services/api/internal/gmail"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
)

type Message struct {
	ID                  uuid.UUID                  `json:"id"`
	ContactID           uuid.UUID                  `json:"contact_id"`
	ContactName         string                     `json:"contact_name"`
	JobID               *uuid.UUID                 `json:"job_id"`
	ApplicationID       *uuid.UUID                 `json:"application_id"`
	ParentID            *uuid.UUID                 `json:"parent_id"`
	Kind                string                     `json:"kind"`
	Status              string                     `json:"status"`
	Origin              string                     `json:"origin"`
	ToEmail             string                     `json:"to_email"`
	Subject             string                     `json:"subject"`
	Body                string                     `json:"body"`
	AttachmentVersionID *uuid.UUID                 `json:"attachment_version_id"`
	Validation          *aiclient.ValidationReport `json:"validation"`
	ApprovedAt          *time.Time                 `json:"approved_at"`
	GmailThreadID       *string                    `json:"gmail_thread_id"`
	SentAt              *time.Time                 `json:"sent_at"`
	ErrorMessage        *string                    `json:"error_message"`
	RepliedAt           *time.Time                 `json:"replied_at"`
	PromptVersion       *string                    `json:"prompt_version"`
	CreatedAt           time.Time                  `json:"created_at"`
	UpdatedAt           time.Time                  `json:"updated_at"`
	// Present on detail reads.
	Replies   []Reply    `json:"replies,omitempty"`
	FollowUps []FollowUp `json:"follow_ups,omitempty"`
	Questions []string   `json:"questions,omitempty"`
	Warnings  []string   `json:"warnings,omitempty"`
}

type Reply struct {
	ID            uuid.UUID  `json:"id"`
	FromAddress   string     `json:"from_address"`
	IsBounce      bool       `json:"is_bounce"`
	ReceivedAt    time.Time  `json:"received_at"`
	ApplicationID *uuid.UUID `json:"application_id"`
}

type FollowUp struct {
	Sequence        int        `json:"sequence"`
	DueAt           time.Time  `json:"due_at"`
	Status          string     `json:"status"`
	DraftID         *uuid.UUID `json:"draft_id"`
	CancelledReason *string    `json:"cancelled_reason"`
}

const msgCols = `m.id, m.contact_id, c.name, m.job_id, m.application_id, m.parent_id, m.kind, m.status, m.origin, m.to_email::text,
	m.subject, m.body, m.attachment_version_id, m.validation, m.approved_at, m.gmail_thread_id, m.sent_at, m.error_message, m.replied_at,
	m.prompt_version, m.created_at, m.updated_at`

func scanMessage(row pgx.Row) (Message, error) {
	var m Message
	var val []byte
	err := row.Scan(&m.ID, &m.ContactID, &m.ContactName, &m.JobID, &m.ApplicationID, &m.ParentID, &m.Kind, &m.Status, &m.Origin, &m.ToEmail,
		&m.Subject, &m.Body, &m.AttachmentVersionID, &val, &m.ApprovedAt, &m.GmailThreadID, &m.SentAt, &m.ErrorMessage, &m.RepliedAt,
		&m.PromptVersion, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, errMessageNotFound
	}
	if err == nil && val != nil {
		m.Validation = &aiclient.ValidationReport{}
		err = json.Unmarshal(val, m.Validation)
	}
	return m, err
}

var errMessageNotFound = httpx.NewError(httpx.CodeNotFound, "Message not found.")

const msgFrom = ` FROM outreach_messages m JOIN recruiter_contacts c ON c.id = m.contact_id`

func getMessage(ctx context.Context, q db.Querier, userID, id uuid.UUID) (Message, error) {
	return scanMessage(q.QueryRow(ctx, `SELECT `+msgCols+msgFrom+` WHERE m.id = $1 AND m.user_id = $2`, id, userID))
}

type MessageFilter struct {
	Status        string
	ContactID     *uuid.UUID
	ApplicationID *uuid.UUID
	JobID         *uuid.UUID
}

func (s *Service) ListMessages(ctx context.Context, userID uuid.UUID, f MessageFilter) ([]Message, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+msgCols+msgFrom+` WHERE m.user_id = $1 AND ($2 = '' OR m.status = $2)
		AND ($3::uuid IS NULL OR m.contact_id = $3) AND ($4::uuid IS NULL OR m.application_id = $4) AND ($5::uuid IS NULL OR m.job_id = $5)
		ORDER BY m.updated_at DESC LIMIT 500`, userID, f.Status, f.ContactID, f.ApplicationID, f.JobID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Message, error) { return scanMessage(r) })
	if out == nil {
		out = []Message{}
	}
	return out, err
}

// GetMessage returns a message with its replies and follow-up schedule.
func (s *Service) GetMessage(ctx context.Context, userID, id uuid.UUID) (Message, error) {
	m, err := getMessage(ctx, s.pool, userID, id)
	if err != nil {
		return m, err
	}
	rows, err := s.pool.Query(ctx, `SELECT r.id, r.from_address, r.is_bounce, r.received_at, r.application_id FROM outreach_replies r
		WHERE r.message_id = $1 ORDER BY r.received_at`, id)
	if err != nil {
		return m, err
	}
	if m.Replies, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Reply, error) {
		var x Reply
		return x, r.Scan(&x.ID, &x.FromAddress, &x.IsBounce, &x.ReceivedAt, &x.ApplicationID)
	}); err != nil {
		return m, err
	}
	rows, err = s.pool.Query(ctx, `SELECT sequence, due_at, status, draft_id, cancelled_reason FROM outreach_follow_ups WHERE message_id = $1 ORDER BY sequence`, id)
	if err != nil {
		return m, err
	}
	m.FollowUps, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (FollowUp, error) {
		var f FollowUp
		return f, r.Scan(&f.Sequence, &f.DueAt, &f.Status, &f.DraftID, &f.CancelledReason)
	})
	return m, err
}

// ---- Drafting ----

type DraftInput struct {
	ContactID           uuid.UUID  `json:"contact_id"`
	JobID               *uuid.UUID `json:"job_id"`
	ApplicationID       *uuid.UUID `json:"application_id"`
	Mode                string     `json:"mode"` // AI | TEMPLATE
	Notes               string     `json:"notes"`
	AttachmentVersionID *uuid.UUID `json:"attachment_version_id"`
}

type jobInfo struct {
	Title, Company, Text string
}

func (s *Service) loadJob(ctx context.Context, jobID uuid.UUID) (jobInfo, error) {
	var j jobInfo
	err := s.pool.QueryRow(ctx, `SELECT title, company_name, left(description_text, 12000) FROM jobs WHERE id = $1`, jobID).Scan(&j.Title, &j.Company, &j.Text)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, httpx.NewError(httpx.CodeNotFound, "Job not found.")
	}
	return j, err
}

func firstName(name string) string {
	f := strings.Fields(name)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

func assemble(greeting string, paragraphs []string, attachment bool, signOff, name, signature string) string {
	parts := []string{greeting}
	parts = append(parts, paragraphs...)
	if attachment {
		parts = append(parts, "I've attached my CV for reference.")
	}
	closing := signOff + "\n" + name
	if signature != "" {
		closing += "\n" + signature
	}
	parts = append(parts, closing)
	return strings.Join(parts, "\n\n")
}

// CreateDraft prepares an email for the user to review. Nothing is sent.
func (s *Service) CreateDraft(ctx context.Context, userID uuid.UUID, in DraftInput, ip string) (Message, error) {
	if in.Mode == "" {
		in.Mode = "TEMPLATE"
	}
	if in.Mode != "AI" && in.Mode != "TEMPLATE" {
		return Message{}, httpx.Validation(map[string]string{"mode": "Choose AI or TEMPLATE."})
	}
	if utf8.RuneCountInString(in.Notes) > 2000 {
		return Message{}, httpx.Validation(map[string]string{"notes": "Use at most 2000 characters."})
	}
	c, err := getContact(ctx, s.pool, userID, in.ContactID)
	if err != nil {
		return Message{}, err
	}
	if c.Email == nil {
		return Message{}, httpx.Validation(map[string]string{"contact_id": "This contact has no email address. Add one you know is correct; addresses are never guessed."})
	}
	if c.DoNotContact {
		return Message{}, httpx.NewError(httpx.CodeConflict, "This contact is marked do-not-contact.")
	}
	if in.JobID == nil && c.JobID != nil {
		in.JobID = c.JobID
	}
	if in.ApplicationID != nil {
		var job uuid.UUID
		if err := s.pool.QueryRow(ctx, `SELECT job_id FROM applications WHERE id = $1 AND user_id = $2`, *in.ApplicationID, userID).Scan(&job); err != nil {
			return Message{}, httpx.Validation(map[string]string{"application_id": "Unknown application."})
		}
		if in.JobID == nil {
			in.JobID = &job
		}
	} else if in.JobID != nil {
		var app uuid.UUID
		if err := s.pool.QueryRow(ctx, `SELECT id FROM applications WHERE user_id = $1 AND job_id = $2`, userID, *in.JobID).Scan(&app); err == nil {
			in.ApplicationID = &app
		}
	}
	if in.AttachmentVersionID != nil {
		if _, _, _, err := s.prep.ApprovedAttachment(ctx, userID, *in.AttachmentVersionID); err != nil {
			return Message{}, err
		}
	}
	var job jobInfo
	if in.JobID != nil {
		if job, err = s.loadJob(ctx, *in.JobID); err != nil {
			return Message{}, err
		}
	}
	facts, cand, err := s.prep.FactSheet(ctx, userID, in.Notes)
	if err != nil {
		return Message{}, err
	}
	set, err := loadSettings(ctx, s.pool, userID)
	if err != nil {
		return Message{}, err
	}

	var subject string
	var paragraphs, questions, warnings []string
	var validation *aiclient.ValidationReport
	var usage *aiclient.AnalyzeUsage
	greeting, signOff := "Hello,", "Best regards,"
	if fn := firstName(c.Name); fn != "" {
		greeting = "Hi " + fn + ","
	}
	if in.Mode == "AI" {
		if err := s.requireLLM(ctx); err != nil {
			return Message{}, err
		}
		resp, err := s.ai.DraftOutreach(ctx, aiclient.OutreachDraftRequest{Kind: "INTRODUCTION",
			Job: aiclient.JobContext{Title: job.Title, Company: job.Company, Text: job.Text, Skills: []string{}}, Facts: facts, Candidate: cand,
			Contact: aiclient.OutreachContact{Name: c.Name, Title: c.Title, Company: c.Company}})
		if err != nil {
			var ae *aiclient.Error
			if errors.As(err, &ae) {
				return Message{}, &httpx.Error{Code: httpx.CodeUnavailable, Message: "The AI service could not draft the email. Try the template instead.", Cause: err}
			}
			return Message{}, err
		}
		subject, paragraphs, questions, warnings = resp.Subject, resp.Paragraphs, resp.Questions, resp.Warnings
		greeting, signOff = resp.Greeting, resp.SignOff
		v := resp.Validation
		validation, usage = &v, &resp.Usage
	} else {
		// Deterministic template from the user's own words; no claims added.
		role := "the open role"
		if job.Title != "" {
			role = "the " + job.Title + " role"
			if job.Company != "" {
				role += " at " + job.Company
			}
		}
		paragraphs = []string{"I'm writing about " + role + "."}
		for _, f := range facts {
			if f.Ref == "basics:headline" {
				paragraphs = append(paragraphs, "A little about me: "+strings.TrimRight(f.Text, ".")+".")
				break
			}
		}
		if n := strings.TrimSpace(in.Notes); n != "" {
			paragraphs = append(paragraphs, n)
		}
		paragraphs = append(paragraphs, "Would you be open to a short conversation, or could you point me to the right person?")
		subject = "Interest in " + strings.TrimPrefix(role, "the ")
		if utf8.RuneCountInString(subject) > 150 {
			subject = string([]rune(subject)[:150])
		}
	}
	if subject == "" {
		subject = "Introduction"
	}
	body := assemble(greeting, paragraphs, in.AttachmentVersionID != nil, signOff, cand.Name, set.Signature)
	if validation == nil {
		validation = s.validateBody(ctx, body, facts, cand)
	}
	var promptVersion, model *string
	if usage != nil {
		promptVersion, model = &usage.PromptVersion, &usage.Model
	}
	origin := "TEMPLATE"
	if in.Mode == "AI" {
		origin = "AI"
	}
	id := ids.New()
	valJSON, _ := json.Marshal(validation)
	if validation == nil {
		valJSON = nil
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO outreach_messages (id, user_id, contact_id, job_id, application_id, kind, status, origin, to_email,
		subject, body, attachment_version_id, validation, prompt_version, model) VALUES ($1,$2,$3,$4,$5,'INTRODUCTION','DRAFT',$6,$7,$8,$9,$10,$11,$12,$13)`,
		id, userID, c.ID, in.JobID, in.ApplicationID, origin, *c.Email, subject, body, in.AttachmentVersionID, valJSON, promptVersion, model)
	if err != nil {
		return Message{}, err
	}
	_ = audit.Record(ctx, s.pool, audit.Event{UserID: &userID, Action: "outreach.draft.create", SubjectType: "outreach_message", SubjectID: &id,
		Outcome: audit.Success, IP: ip, Metadata: map[string]any{"mode": in.Mode, "contact_id": c.ID, "has_attachment": in.AttachmentVersionID != nil}})
	m, err := s.GetMessage(ctx, userID, id)
	m.Questions, m.Warnings = questions, warnings
	return m, err
}

func (s *Service) requireLLM(ctx context.Context) error {
	if s.ai == nil {
		return httpx.NewError(httpx.CodeUnavailable, "The AI service is not configured on this server.")
	}
	ictx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	info, err := s.ai.Info(ictx)
	if err != nil {
		return &httpx.Error{Code: httpx.CodeUnavailable, Message: "The AI service is unreachable. Try the template instead.", Cause: err}
	}
	if !info.LLMConfigured || !info.Has("outreach.draft") {
		return httpx.NewError(httpx.CodeAIUnavailable, "No AI provider is configured, so AI drafts are unavailable. Use the template instead.")
	}
	return nil
}

var blankLines = regexp.MustCompile(`\n\s*\n`)

// validateBody fact-checks free text (each paragraph against the whole
// profile). Uncited-text warnings are dropped: emails are not citation-bound.
func (s *Service) validateBody(ctx context.Context, body string, facts []aiclient.Fact, cand aiclient.Candidate) *aiclient.ValidationReport {
	if s.ai == nil {
		return nil
	}
	type block struct {
		ID   string            `json:"id"`
		Type string            `json:"type"`
		Text string            `json:"text"`
		Refs []string          `json:"refs"`
		Meta map[string]string `json:"meta"`
	}
	var blocks []block
	for i, p := range blankLines.Split(body, -1) {
		if p = strings.TrimSpace(p); p != "" {
			blocks = append(blocks, block{ID: fmt.Sprintf("p%d", i+1), Type: "paragraph", Text: p, Refs: []string{}, Meta: map[string]string{}})
		}
	}
	content, _ := json.Marshal(map[string]any{"schema_version": "doc-v1", "kind": "COVER_LETTER",
		"sections": []any{map[string]any{"key": "email", "title": "Email", "blocks": blocks}}})
	vctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rep, err := s.ai.ValidateDocument(vctx, aiclient.ValidateDocumentRequest{Content: content, Facts: facts, Candidate: cand})
	if err != nil {
		s.log.Warn("outreach validation unavailable", "err", err)
		return nil
	}
	kept := []aiclient.Issue{}
	for _, is := range rep.Issues {
		if is.Code != "NO_SUPPORT" && is.Code != "PLACEHOLDER" {
			kept = append(kept, is)
		}
	}
	rep.Issues = kept
	rep.Status = "PASSED"
	for _, is := range kept {
		if is.Severity == "ERROR" {
			rep.Status = "FAILED"
			break
		}
		rep.Status = "WARNINGS"
	}
	return &rep
}

// ---- Edit / approve / cancel ----

type EditInput struct {
	Subject             string     `json:"subject"`
	Body                string     `json:"body"`
	AttachmentVersionID *uuid.UUID `json:"attachment_version_id"`
}

func lockMessage(ctx context.Context, tx pgx.Tx, userID, id uuid.UUID) (Message, error) {
	return scanMessage(tx.QueryRow(ctx, `SELECT `+msgCols+msgFrom+` WHERE m.id = $1 AND m.user_id = $2 FOR UPDATE OF m`, id, userID))
}

// Edit changes a draft. Editing an approved message withdraws the approval:
// approval is bound to the exact content.
func (s *Service) Edit(ctx context.Context, userID, id uuid.UUID, in EditInput, ip string) (Message, error) {
	errs := map[string]string{}
	subject := limitText(errs, "subject", in.Subject, 200)
	body := strings.TrimSpace(strings.ReplaceAll(in.Body, "\r\n", "\n"))
	if subject == "" {
		errs["subject"] = "Required."
	}
	if strings.ContainsAny(subject, "\r\n") {
		errs["subject"] = "The subject must be one line."
	}
	if body == "" || utf8.RuneCountInString(body) > 10000 {
		errs["body"] = "Write the email (at most 10,000 characters)."
	}
	if len(errs) > 0 {
		return Message{}, httpx.Validation(errs)
	}
	if in.AttachmentVersionID != nil {
		if _, _, _, err := s.prep.ApprovedAttachment(ctx, userID, *in.AttachmentVersionID); err != nil {
			return Message{}, err
		}
	}
	facts, cand, err := s.prep.FactSheet(ctx, userID, "")
	if err != nil {
		return Message{}, err
	}
	val := s.validateBody(ctx, body, facts, cand)
	valJSON, _ := json.Marshal(val)
	if val == nil {
		valJSON = nil
	}
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		m, err := lockMessage(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		if m.Status != "DRAFT" && m.Status != "APPROVED" {
			return httpx.NewError(httpx.CodeInvalidTransition, "Only drafts can be edited.")
		}
		if m.Kind == "FOLLOW_UP" && subject != m.Subject {
			return httpx.Validation(map[string]string{"subject": "Follow-ups keep the original subject so they stay in the same thread."})
		}
		if _, err := tx.Exec(ctx, `UPDATE outreach_messages SET subject = $2, body = $3, attachment_version_id = $4, validation = $5,
			status = 'DRAFT', approved_hash = NULL, approved_at = NULL, origin = CASE WHEN origin = 'FOLLOW_UP_SCHEDULER' THEN origin ELSE 'USER' END,
			updated_at = now() WHERE id = $1`, id, subject, body, in.AttachmentVersionID, valJSON); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "outreach.draft.edit", SubjectType: "outreach_message", SubjectID: &id,
			Outcome: audit.Success, IP: ip, Metadata: map[string]any{"approval_withdrawn": m.Status == "APPROVED"}})
	})
	if err != nil {
		return Message{}, err
	}
	return s.GetMessage(ctx, userID, id)
}

func contentHash(m Message) []byte {
	h := sha256.New()
	att := ""
	if m.AttachmentVersionID != nil {
		att = m.AttachmentVersionID.String()
	}
	for _, p := range []string{strings.ToLower(m.ToEmail), m.Subject, m.Body, att} {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return h.Sum(nil)
}

type ApproveInput struct {
	Confirm           bool `json:"confirm"`
	AcknowledgeIssues bool `json:"acknowledge_issues"`
}

// Approve authorises sending this exact content to this recipient.
func (s *Service) Approve(ctx context.Context, userID, id uuid.UUID, in ApproveInput, ip string) (Message, error) {
	if !in.Confirm {
		return Message{}, httpx.Validation(map[string]string{"confirm": "Confirm that you have read this email and want it sent from your Gmail."})
	}
	err := db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		m, err := lockMessage(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		if m.Status != "DRAFT" {
			return httpx.NewError(httpx.CodeInvalidTransition, "Only drafts can be approved.")
		}
		errorsN := 0
		if m.Validation != nil {
			for _, is := range m.Validation.Issues {
				if is.Severity == "ERROR" {
					errorsN++
				}
			}
		}
		if errorsN > 0 && !in.AcknowledgeIssues {
			return &httpx.Error{Code: httpx.CodeValidationFailed,
				Message: fmt.Sprintf("The fact check found %d statement(s) your profile does not support. Fix them, or confirm you have checked them.", errorsN),
				Details: map[string]string{"acknowledge_issues": "Required to approve with unresolved fact-check errors."}}
		}
		if _, err := tx.Exec(ctx, `UPDATE outreach_messages SET status = 'APPROVED', approved_hash = $2, approved_at = now(), updated_at = now()
			WHERE id = $1`, id, contentHash(m)); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "outreach.message.approve", SubjectType: "outreach_message", SubjectID: &id,
			Outcome: audit.Success, IP: ip, Metadata: map[string]any{"acknowledged_issues": errorsN}})
	})
	if err != nil {
		return Message{}, err
	}
	return s.GetMessage(ctx, userID, id)
}

func (s *Service) Cancel(ctx context.Context, userID, id uuid.UUID, ip string) (Message, error) {
	err := db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		m, err := lockMessage(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		if m.Status != "DRAFT" && m.Status != "APPROVED" {
			return httpx.NewError(httpx.CodeInvalidTransition, "Only unsent messages can be cancelled.")
		}
		if _, err := tx.Exec(ctx, `UPDATE outreach_messages SET status = 'CANCELLED', updated_at = now() WHERE id = $1`, id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "outreach.message.cancel", SubjectType: "outreach_message", SubjectID: &id,
			Outcome: audit.Success, IP: ip})
	})
	if err != nil {
		return Message{}, err
	}
	return s.GetMessage(ctx, userID, id)
}

// MarkNotSent resolves an UNKNOWN outcome after the user checked their Sent
// folder and found nothing. (We cannot record SENT without Gmail's ids.)
func (s *Service) MarkNotSent(ctx context.Context, userID, id uuid.UUID, ip string) (Message, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE outreach_messages SET status = 'FAILED', error_message = 'Marked as not sent by the user after an unconfirmed send.',
		updated_at = now() WHERE id = $1 AND user_id = $2 AND status = 'UNKNOWN'`, id, userID)
	if err != nil {
		return Message{}, err
	}
	if tag.RowsAffected() == 0 {
		return Message{}, httpx.NewError(httpx.CodeInvalidTransition, "Only messages with an unknown outcome can be marked as not sent.")
	}
	_ = audit.Record(ctx, s.pool, audit.Event{UserID: &userID, Action: "outreach.message.mark_not_sent", SubjectType: "outreach_message",
		SubjectID: &id, Outcome: audit.Success, IP: ip})
	return s.GetMessage(ctx, userID, id)
}

// LinkApplication associates a message (and its replies) with an application.
func (s *Service) LinkApplication(ctx context.Context, userID, id uuid.UUID, appID *uuid.UUID) (Message, error) {
	if appID != nil {
		var ok bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM applications WHERE id = $1 AND user_id = $2)`, *appID, userID).Scan(&ok); err != nil {
			return Message{}, err
		}
		if !ok {
			return Message{}, httpx.Validation(map[string]string{"application_id": "Unknown application."})
		}
	}
	err := db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE outreach_messages SET application_id = $3, updated_at = now() WHERE id = $1 AND user_id = $2`, id, userID, appID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errMessageNotFound
		}
		_, err = tx.Exec(ctx, `UPDATE outreach_replies SET application_id = $2 WHERE message_id = $1`, id, appID)
		return err
	})
	if err != nil {
		return Message{}, err
	}
	return s.GetMessage(ctx, userID, id)
}

// ---- Sending ----

var keyRe = regexp.MustCompile(`^[A-Za-z0-9_.:-]{8,100}$`)

// Send delivers an approved message through the user's Gmail. Every check
// runs immediately before sending; the send itself happens after the row is
// marked SENDING, so a crash can never cause a silent duplicate.
func (s *Service) Send(ctx context.Context, userID, id uuid.UUID, key string, actor audit.Actor, ip string) (Message, error) {
	if !keyRe.MatchString(key) {
		return Message{}, httpx.Validation(map[string]string{"idempotency_key": "Send an Idempotency-Key header of 8–100 characters."})
	}
	if s.mail == nil {
		return Message{}, httpx.NewError(httpx.CodeUnavailable, "Gmail is not configured on this server.")
	}
	// Check the connection (and refresh the token) before changing any state,
	// so a missing or expired connection never consumes the approved message.
	from, err := s.mail.From(ctx, userID)
	if err != nil {
		return Message{}, err
	}
	var m Message
	var parentRFC, parentThread, parentSubject *string
	var rfcID string
	replay := false
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		if m, err = lockMessage(ctx, tx, userID, id); err != nil {
			return err
		}
		var existing *string
		if err := tx.QueryRow(ctx, `SELECT idempotency_key FROM outreach_messages WHERE id = $1`, id).Scan(&existing); err != nil {
			return err
		}
		if existing != nil {
			if *existing == key {
				replay = true
				return nil
			}
			return httpx.NewError(httpx.CodeConflict, "This message has already been sent or attempted.")
		}
		var other bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM outreach_messages WHERE user_id = $1 AND idempotency_key = $2)`, userID, key).Scan(&other); err != nil {
			return err
		}
		if other {
			return httpx.NewError(httpx.CodeConflict, "This idempotency key was already used for another message.")
		}
		if m.Status != "APPROVED" {
			return httpx.NewError(httpx.CodeInvalidTransition, "Approve this message before sending it.")
		}
		var approved []byte
		if err := tx.QueryRow(ctx, `SELECT approved_hash FROM outreach_messages WHERE id = $1`, id).Scan(&approved); err != nil {
			return err
		}
		if !bytes.Equal(approved, contentHash(m)) {
			return httpx.NewError(httpx.CodeConflict, "The message changed after it was approved. Review and approve it again.")
		}
		c, err := getContact(ctx, tx, userID, m.ContactID)
		if err != nil {
			return err
		}
		switch {
		case c.DoNotContact:
			return httpx.NewError(httpx.CodeConflict, "This contact is marked do-not-contact.")
		case c.EmailVerification == "INVALID" || c.EmailVerification == "BOUNCED":
			return httpx.NewError(httpx.CodeConflict, "This address is invalid or has bounced. Check the contact's email.")
		case c.Email == nil || !strings.EqualFold(*c.Email, m.ToEmail):
			return httpx.NewError(httpx.CodeConflict, "The contact's address changed after this draft was written. Create a new draft.")
		}
		if err := s.guard.Authorize(ctx, tx, userID, actor, automation.ActionSendEmail); err != nil {
			return automation.AsHTTP(err)
		}
		set, err := loadSettings(ctx, tx, userID)
		if err != nil {
			return err
		}
		var today int
		// Counted by send time (sent_at), or the attempt time for unconfirmed sends.
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM outreach_messages WHERE user_id = $1 AND status IN ('SENDING','SENT','UNKNOWN')
			AND coalesce(sent_at, updated_at) > now() - interval '24 hours'`, userID).Scan(&today); err != nil {
			return err
		}
		if today >= set.DailySendLimit {
			return &httpx.Error{Code: httpx.CodeRateLimited, Message: fmt.Sprintf("Your limit of %d emails per 24 hours has been reached. You can change it in Settings.", set.DailySendLimit)}
		}
		var last *time.Time
		if err := tx.QueryRow(ctx, `SELECT max(sent_at) FROM outreach_messages WHERE user_id = $1 AND to_email = $2 AND status IN ('SENT','UNKNOWN','SENDING')`,
			userID, m.ToEmail).Scan(&last); err != nil {
			return err
		}
		if last != nil && s.now().Sub(*last) < time.Duration(set.RecipientCooldownDays)*24*time.Hour {
			return httpx.NewError(httpx.CodeConflict, fmt.Sprintf("You emailed this address %s. Your cooldown is %d day(s) between emails to the same person.",
				last.Format("2 Jan"), set.RecipientCooldownDays))
		}
		if m.Kind == "INTRODUCTION" {
			var unanswered bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM outreach_messages WHERE user_id = $1 AND contact_id = $2 AND kind = 'INTRODUCTION'
				AND status IN ('SENT','UNKNOWN') AND replied_at IS NULL)`, userID, m.ContactID).Scan(&unanswered); err != nil {
				return err
			}
			if unanswered {
				return httpx.NewError(httpx.CodeConflict, "You already sent this contact an introduction without a reply. Send a follow-up instead.")
			}
		}
		if m.ParentID != nil {
			if err := tx.QueryRow(ctx, `SELECT rfc_message_id, gmail_thread_id, subject FROM outreach_messages WHERE id = $1`, *m.ParentID).
				Scan(&parentRFC, &parentThread, &parentSubject); err != nil {
				return err
			}
		}
		rfcID = fmt.Sprintf("<%s@careerpilot.app>", ids.New())
		if _, err := tx.Exec(ctx, `UPDATE outreach_messages SET status = 'SENDING', idempotency_key = $2, rfc_message_id = $3, updated_at = now()
			WHERE id = $1`, id, key, rfcID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Actor: actor, Action: "outreach.send.start", SubjectType: "outreach_message",
			SubjectID: &id, Outcome: audit.Success, IP: ip})
	})
	if err != nil {
		return Message{}, err
	}
	if replay {
		return s.GetMessage(ctx, userID, id)
	}

	finish := func(status, errMsg, gmailID, threadID string) (Message, error) {
		ctx := context.WithoutCancel(ctx)
		err := db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
			if status == "SENT" {
				if _, err := tx.Exec(ctx, `UPDATE outreach_messages SET status = 'SENT', gmail_message_id = $2, gmail_thread_id = $3, sent_at = now(),
					error_message = NULL, updated_at = now() WHERE id = $1`, id, gmailID, threadID); err != nil {
					return err
				}
				if err := s.scheduleFollowUps(ctx, tx, userID, m); err != nil {
					return err
				}
			} else if _, err := tx.Exec(ctx, `UPDATE outreach_messages SET status = $2, error_message = $3, updated_at = now() WHERE id = $1`,
				id, status, errMsg); err != nil {
				return err
			}
			outcome := audit.Success
			if status != "SENT" {
				outcome = audit.Failure
			}
			return audit.Record(ctx, tx, audit.Event{UserID: &userID, Actor: actor, Action: "outreach.send", SubjectType: "outreach_message",
				SubjectID: &id, Outcome: outcome, IP: ip, Metadata: map[string]any{"status": status, "has_attachment": m.AttachmentVersionID != nil}})
		})
		if err != nil {
			return Message{}, err
		}
		return s.GetMessage(ctx, userID, id)
	}

	var attachment []byte
	var attName string
	if m.AttachmentVersionID != nil {
		if attachment, attName, _, err = s.prep.ApprovedAttachment(ctx, userID, *m.AttachmentVersionID); err != nil {
			return finish("FAILED", "The attached document is no longer approved.", "", "")
		}
	}
	subject, thread := m.Subject, ""
	if parentSubject != nil {
		subject = *parentSubject
		if !strings.HasPrefix(strings.ToLower(subject), "re:") {
			subject = "Re: " + subject
		}
	}
	if parentThread != nil {
		thread = *parentThread
	}
	raw := compose(from, m.ToEmail, subject, m.Body, rfcID, parentRFC, attachment, attName, s.now())
	gmailID, threadID, err := s.mail.Send(ctx, userID, raw, thread)
	if err != nil {
		var ae *gmail.APIError
		if errors.As(err, &ae) && !ae.Definite {
			return finish("UNKNOWN", "Gmail did not confirm the send. Check your Gmail Sent folder before trying again.", "", "")
		}
		return finish("FAILED", errText(err), "", "")
	}
	return finish("SENT", "", gmailID, threadID)
}

func errText(err error) string {
	var he *httpx.Error
	if errors.As(err, &he) {
		return he.Message
	}
	var ae *gmail.APIError
	if errors.As(err, &ae) {
		if ae.Message != "" {
			return "Gmail rejected the message: " + ae.Message
		}
		return fmt.Sprintf("Gmail rejected the message (status %d).", ae.Status)
	}
	return "The message could not be sent."
}

func (s *Service) scheduleFollowUps(ctx context.Context, tx pgx.Tx, userID uuid.UUID, m Message) error {
	if m.Kind != "INTRODUCTION" {
		return nil
	}
	set, err := loadSettings(ctx, tx, userID)
	if err != nil || !set.FollowUpsEnabled {
		return err
	}
	for n := 1; n <= set.MaxFollowUps; n++ {
		due := s.now().Add(time.Duration(n*set.FollowUpAfterDays) * 24 * time.Hour)
		if _, err := tx.Exec(ctx, `INSERT INTO outreach_follow_ups (id, user_id, message_id, sequence, due_at, status)
			VALUES ($1, $2, $3, $4, $5, 'SCHEDULED') ON CONFLICT DO NOTHING`, ids.New(), userID, m.ID, n, due); err != nil {
			return err
		}
	}
	return nil
}

// compose builds an RFC 5322 message: plain text, optional DOCX attachment.
func compose(from, to, subject, body, msgID string, inReplyTo *string, attachment []byte, attName string, now time.Time) []byte {
	var b bytes.Buffer
	h := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	h("From", from)
	h("To", to)
	h("Subject", mime.QEncoding.Encode("utf-8", subject))
	h("Date", now.Format(time.RFC1123Z))
	h("Message-ID", msgID)
	if inReplyTo != nil {
		h("In-Reply-To", *inReplyTo)
		h("References", *inReplyTo)
	}
	h("MIME-Version", "1.0")
	text := base64Lines([]byte(strings.ReplaceAll(body, "\n", "\r\n")))
	if attachment == nil {
		h("Content-Type", `text/plain; charset="utf-8"`)
		h("Content-Transfer-Encoding", "base64")
		b.WriteString("\r\n" + text)
		return b.Bytes()
	}
	boundary := "cp-" + strings.ReplaceAll(ids.New().String(), "-", "")
	h("Content-Type", `multipart/mixed; boundary="`+boundary+`"`)
	b.WriteString("\r\n--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\nContent-Transfer-Encoding: base64\r\n\r\n" + text)
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: application/vnd.openxmlformats-officedocument.wordprocessingml.document\r\n")
	b.WriteString(`Content-Disposition: attachment; filename="` + attName + "\"\r\nContent-Transfer-Encoding: base64\r\n\r\n")
	b.WriteString(base64Lines(attachment))
	b.WriteString("--" + boundary + "--\r\n")
	return b.Bytes()
}

func base64Lines(data []byte) string {
	enc := base64.StdEncoding.EncodeToString(data)
	var b strings.Builder
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc + "\r\n")
	return b.String()
}
