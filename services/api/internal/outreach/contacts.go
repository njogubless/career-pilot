// Package outreach manages recruiter contacts and personal emails sent
// through the user's own Gmail: grounded drafts, explicit approval bound to
// the exact content, guarded and idempotent sending, reply detection,
// follow-ups that respect the user's preferences, and do-not-contact
// (master prompt §11, ADR-023).
package outreach

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/careerpilot/careerpilot/services/api/internal/audit"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
)

type Contact struct {
	ID                 uuid.UUID  `json:"id"`
	Name               string     `json:"name"`
	Title              string     `json:"title"`
	Company            string     `json:"company"`
	JobID              *uuid.UUID `json:"job_id"`
	ProfileURL         string     `json:"profile_url"`
	Email              *string    `json:"email"`
	Source             string     `json:"source"`
	SourceEvidence     string     `json:"source_evidence"`
	EmailVerification  string     `json:"email_verification"`
	VerifiedAt         *time.Time `json:"verified_at"`
	Confidence         string     `json:"confidence"`
	DoNotContact       bool       `json:"do_not_contact"`
	DoNotContactReason string     `json:"do_not_contact_reason"`
	Notes              string     `json:"notes"`
	DiscoveredAt       time.Time  `json:"discovered_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	MessagesSent       int        `json:"messages_sent"`
	LastContactedAt    *time.Time `json:"last_contacted_at"`
}

const contactCols = `c.id, c.name, c.title, c.company, c.job_id, c.profile_url, c.email::text, c.source, c.source_evidence,
	c.email_verification, c.verified_at, c.confidence, c.do_not_contact, c.do_not_contact_reason, c.notes, c.discovered_at, c.updated_at,
	(SELECT count(*) FROM outreach_messages m WHERE m.contact_id = c.id AND m.status = 'SENT'),
	(SELECT max(m.sent_at) FROM outreach_messages m WHERE m.contact_id = c.id AND m.status = 'SENT')`

func scanContact(row pgx.Row) (Contact, error) {
	var c Contact
	err := row.Scan(&c.ID, &c.Name, &c.Title, &c.Company, &c.JobID, &c.ProfileURL, &c.Email, &c.Source, &c.SourceEvidence,
		&c.EmailVerification, &c.VerifiedAt, &c.Confidence, &c.DoNotContact, &c.DoNotContactReason, &c.Notes, &c.DiscoveredAt, &c.UpdatedAt,
		&c.MessagesSent, &c.LastContactedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, errContactNotFound
	}
	return c, err
}

var errContactNotFound = httpx.NewError(httpx.CodeNotFound, "Contact not found.")

func getContact(ctx context.Context, q db.Querier, userID, id uuid.UUID) (Contact, error) {
	return scanContact(q.QueryRow(ctx, `SELECT `+contactCols+` FROM recruiter_contacts c WHERE c.id = $1 AND c.user_id = $2`, id, userID))
}

func (s *Service) ListContacts(ctx context.Context, userID uuid.UUID, jobID *uuid.UUID) ([]Contact, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+contactCols+` FROM recruiter_contacts c WHERE c.user_id = $1 AND ($2::uuid IS NULL OR c.job_id = $2)
		ORDER BY c.updated_at DESC LIMIT 500`, userID, jobID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Contact, error) { return scanContact(r) })
	if out == nil {
		out = []Contact{}
	}
	return out, err
}

func (s *Service) GetContact(ctx context.Context, userID, id uuid.UUID) (Contact, error) {
	return getContact(ctx, s.pool, userID, id)
}

type ContactInput struct {
	Name               string     `json:"name"`
	Title              string     `json:"title"`
	Company            string     `json:"company"`
	JobID              *uuid.UUID `json:"job_id"`
	ProfileURL         string     `json:"profile_url"`
	Email              string     `json:"email"`
	Notes              string     `json:"notes"`
	DoNotContact       bool       `json:"do_not_contact"`
	DoNotContactReason string     `json:"do_not_contact_reason"`
}

func limitText(errs map[string]string, field, v string, max int) string {
	v = strings.TrimSpace(v)
	if utf8.RuneCountInString(v) > max {
		errs[field] = fmt.Sprintf("Use at most %d characters.", max)
	}
	return v
}

// normalizeEmail validates syntax only; it never invents or completes an address.
func normalizeEmail(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	a, err := mail.ParseAddress(raw)
	if err != nil || a.Address != raw || !strings.Contains(raw[strings.LastIndex(raw, "@")+1:], ".") || len(raw) > 320 {
		return "", false
	}
	return strings.ToLower(raw), true
}

func (in *ContactInput) clean() (*string, error) {
	errs := map[string]string{}
	in.Name = limitText(errs, "name", in.Name, 200)
	in.Title = limitText(errs, "title", in.Title, 200)
	in.Company = limitText(errs, "company", in.Company, 200)
	in.Notes = limitText(errs, "notes", in.Notes, 2000)
	in.DoNotContactReason = limitText(errs, "do_not_contact_reason", in.DoNotContactReason, 500)
	in.ProfileURL = limitText(errs, "profile_url", in.ProfileURL, 500)
	if in.ProfileURL != "" && !strings.HasPrefix(in.ProfileURL, "https://") && !strings.HasPrefix(in.ProfileURL, "http://") {
		errs["profile_url"] = "Enter a full http(s) URL."
	}
	var email *string
	if strings.TrimSpace(in.Email) != "" {
		e, ok := normalizeEmail(in.Email)
		if !ok {
			errs["email"] = "Enter a valid email address."
		}
		email = &e
	}
	if in.Name == "" && email == nil {
		errs["name"] = "Give at least a name or an email address."
	}
	if len(errs) > 0 {
		return nil, httpx.Validation(errs)
	}
	return email, nil
}

func dupErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return httpx.Validation(map[string]string{"email": "You already have a contact with this email address."})
	}
	return err
}

// CreateContact stores a contact the user provides. The address is checked
// for syntax and its domain for mail records; it is never "verified" further.
func (s *Service) CreateContact(ctx context.Context, userID uuid.UUID, in ContactInput, ip string) (Contact, error) {
	email, err := in.clean()
	if err != nil {
		return Contact{}, err
	}
	if in.JobID != nil {
		if err := s.jobExists(ctx, *in.JobID); err != nil {
			return Contact{}, err
		}
	}
	id := ids.New()
	_, err = s.pool.Exec(ctx, `INSERT INTO recruiter_contacts (id, user_id, name, title, company, job_id, profile_url, email, source, confidence,
		do_not_contact, do_not_contact_reason, notes) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'USER_PROVIDED','HIGH',$9,$10,$11)`,
		id, userID, in.Name, in.Title, in.Company, in.JobID, in.ProfileURL, email, in.DoNotContact, in.DoNotContactReason, in.Notes)
	if err != nil {
		return Contact{}, dupErr(err)
	}
	_ = audit.Record(ctx, s.pool, audit.Event{UserID: &userID, Action: "outreach.contact.create", SubjectType: "contact", SubjectID: &id,
		Outcome: audit.Success, IP: ip, Metadata: map[string]any{"has_email": email != nil, "source": "USER_PROVIDED"}})
	if email != nil {
		s.verify(ctx, userID, id, *email)
	}
	return getContact(ctx, s.pool, userID, id)
}

func (s *Service) jobExists(ctx context.Context, jobID uuid.UUID) error {
	var ok bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM jobs WHERE id = $1)`, jobID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return httpx.NewError(httpx.CodeNotFound, "Job not found.")
	}
	return nil
}

// UpdateContact edits a contact. Changing the address resets verification.
// Marking do-not-contact cancels scheduled follow-ups to them.
func (s *Service) UpdateContact(ctx context.Context, userID, id uuid.UUID, in ContactInput, ip string) (Contact, error) {
	email, err := in.clean()
	if err != nil {
		return Contact{}, err
	}
	old, err := getContact(ctx, s.pool, userID, id)
	if err != nil {
		return Contact{}, err
	}
	changed := (old.Email == nil) != (email == nil) || (email != nil && old.Email != nil && *old.Email != *email)
	if changed && old.MessagesSent > 0 {
		return Contact{}, httpx.Validation(map[string]string{"email": "This address was already emailed. Add a new contact for a different address."})
	}
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE recruiter_contacts SET name=$3, title=$4, company=$5, job_id=$6, profile_url=$7, email=$8, notes=$9,
			do_not_contact=$10, do_not_contact_reason=$11, updated_at=now(),
			email_verification = CASE WHEN $12 THEN 'UNVERIFIED' ELSE email_verification END,
			verified_at = CASE WHEN $12 THEN NULL ELSE verified_at END,
			source = CASE WHEN $12 THEN 'USER_PROVIDED' ELSE source END
			WHERE id = $1 AND user_id = $2`, id, userID, in.Name, in.Title, in.Company, in.JobID, in.ProfileURL, email, in.Notes,
			in.DoNotContact, in.DoNotContactReason, changed); err != nil {
			return dupErr(err)
		}
		if in.DoNotContact && !old.DoNotContact {
			if err := cancelFollowUps(ctx, tx, `m.contact_id = $1`, id, "Contact marked do-not-contact"); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "outreach.contact.update", SubjectType: "contact", SubjectID: &id,
			Outcome: audit.Success, IP: ip, Metadata: map[string]any{"email_changed": changed, "do_not_contact": in.DoNotContact}})
	})
	if err != nil {
		return Contact{}, err
	}
	if changed && email != nil {
		s.verify(ctx, userID, id, *email)
	}
	return getContact(ctx, s.pool, userID, id)
}

func (s *Service) DeleteContact(ctx context.Context, userID, id uuid.UUID, ip string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM recruiter_contacts WHERE id = $1 AND user_id = $2`, id, userID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return httpx.NewError(httpx.CodeConflict, "This contact has messages, which are kept as records. Mark them do-not-contact instead.")
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errContactNotFound
	}
	return audit.Record(ctx, s.pool, audit.Event{UserID: &userID, Action: "outreach.contact.delete", SubjectType: "contact", SubjectID: &id,
		Outcome: audit.Success, IP: ip})
}

// ---- Verification ----

// Resolver looks up mail records (net.DefaultResolver in production).
type Resolver interface {
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// verify checks syntax and whether the domain accepts mail (MX, or an
// address record as the implicit MX). It never contacts the mailbox:
// SMTP probing is unreliable and looks like spam. DNS timeouts leave the
// contact UNVERIFIED rather than guessing.
func (s *Service) verify(ctx context.Context, userID, id uuid.UUID, email string) string {
	status := "INVALID"
	if _, ok := normalizeEmail(email); ok {
		domain := email[strings.LastIndex(email, "@")+1:]
		vctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		mx, err := s.resolver.LookupMX(vctx, domain)
		nullMX := err == nil && len(mx) == 1 && strings.TrimSuffix(mx[0].Host, ".") == "" // RFC 7505: accepts no mail
		switch {
		case nullMX:
			status = "INVALID"
		case err == nil && len(mx) > 0:
			status = "DOMAIN_OK"
		case err == nil || isNotFound(err):
			// No MX: an address record acts as the implicit MX (RFC 5321).
			hosts, herr := s.resolver.LookupHost(vctx, domain)
			switch {
			case herr == nil && len(hosts) > 0:
				status = "DOMAIN_OK"
			case herr != nil && !isNotFound(herr):
				return "UNVERIFIED"
			}
		default:
			s.log.Warn("email domain lookup failed", "err", err)
			return "UNVERIFIED"
		}
	}
	_, _ = s.pool.Exec(ctx, `UPDATE recruiter_contacts SET email_verification = $3, verified_at = now(), updated_at = now()
		WHERE id = $1 AND user_id = $2 AND email_verification <> 'BOUNCED'`, id, userID, status)
	return status
}

func isNotFound(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && dnsErr.IsNotFound
}

func (s *Service) VerifyContact(ctx context.Context, userID, id uuid.UUID) (Contact, error) {
	c, err := getContact(ctx, s.pool, userID, id)
	if err != nil {
		return Contact{}, err
	}
	if c.Email == nil {
		return Contact{}, httpx.Validation(map[string]string{"email": "This contact has no email address to check."})
	}
	if c.EmailVerification == "BOUNCED" {
		return c, nil // a bounce is stronger evidence than DNS
	}
	s.verify(ctx, userID, id, *c.Email)
	return getContact(ctx, s.pool, userID, id)
}

// ---- Discovery from job postings ----

var (
	emailRe = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	// Addresses for other purposes are not recruiting contacts.
	nonRecruiting = regexp.MustCompile(`(?i)^(no-?reply|do-?not-?reply|privacy|legal|accommodations?|accessibility|abuse|security|dpo|gdpr|support|help|info|press|media|ir|investors?)@`)
	recruitingCtx = regexp.MustCompile(`(?i)\b(apply|application|applications|resume|résumé|cv|recruit\w*|talent|hiring|questions|careers?|jobs?)\b`)
	// Sentences that introduce an address for another purpose. Measured on
	// 762 live postings (2026-11-17): 761 published addresses were for
	// accommodation requests, fraud reports or privacy, not recruiting.
	nonRecruitingCtx = regexp.MustCompile(`(?i)accommodat|adjustment|disabilit|accessib|equal (employment|opportunity)|\beeo\b|privacy|` +
		`personal (data|information)|gdpr|data protection|fraud|scam|phishing|suspicious|impersonat|unsolicited|agenc(y|ies)|` +
		`whistleblow|ethics|complain|report`)
)

// Discovered is an address found verbatim in a job posting.
type Discovered struct {
	Contact Contact `json:"contact"`
	Created bool    `json:"created"`
}

// DiscoverFromJob records email addresses that the posting itself publishes.
// Nothing is guessed: no name/role is inferred, and addresses are kept as
// UNVERIFIED (then checked by DNS) with the surrounding text as evidence.
func (s *Service) DiscoverFromJob(ctx context.Context, userID, jobID uuid.UUID, ip string) ([]Discovered, error) {
	var text, company string
	err := s.pool.QueryRow(ctx, `SELECT description_text, company_name FROM jobs WHERE id = $1`, jobID).Scan(&text, &company)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httpx.NewError(httpx.CodeNotFound, "Job not found.")
	}
	if err != nil {
		return nil, err
	}
	out := []Discovered{}
	seen := map[string]bool{}
	for _, loc := range emailRe.FindAllStringIndex(text, 20) {
		addr := strings.TrimRight(text[loc[0]:loc[1]], ".")
		email, ok := normalizeEmail(addr)
		if !ok || seen[email] || nonRecruiting.MatchString(email) || strings.HasSuffix(email, "@example.com") {
			continue
		}
		seen[email] = true
		from, to := max(0, loc[0]-160), min(len(text), loc[1]+60)
		lead := text[from:loc[0]] // the sentence that introduces the address
		if nonRecruitingCtx.MatchString(lead) {
			continue
		}
		evidence := strings.Join(strings.Fields(strings.ToValidUTF8(text[from:to], "")), " ")
		confidence := "LOW"
		if recruitingCtx.MatchString(evidence) {
			confidence = "MEDIUM"
		}
		id := ids.New()
		tag, err := s.pool.Exec(ctx, `INSERT INTO recruiter_contacts (id, user_id, company, job_id, email, source, source_evidence, confidence)
			VALUES ($1, $2, $3, $4, $5, 'JOB_POSTING', $6, $7) ON CONFLICT (user_id, email) WHERE email IS NOT NULL DO NOTHING`,
			id, userID, company, jobID, email, evidence, confidence)
		if err != nil {
			return nil, err
		}
		created := tag.RowsAffected() == 1
		if created {
			s.verify(ctx, userID, id, email)
		} else if err := s.pool.QueryRow(ctx, `SELECT id FROM recruiter_contacts WHERE user_id = $1 AND email = $2`, userID, email).Scan(&id); err != nil {
			return nil, err
		}
		c, err := getContact(ctx, s.pool, userID, id)
		if err != nil {
			return nil, err
		}
		out = append(out, Discovered{Contact: c, Created: created})
	}
	_ = audit.Record(ctx, s.pool, audit.Event{UserID: &userID, Action: "outreach.contact.discover", SubjectType: "job", SubjectID: &jobID,
		Outcome: audit.Success, IP: ip, Metadata: map[string]any{"found": len(out)}})
	return out, nil
}
