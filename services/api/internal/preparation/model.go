// Package preparation builds tailored application documents (CV, cover
// letter, application answers) from the user's verified profile facts, keeps
// every change as an immutable version, validates claims against the facts
// and records explicit user approval (ADR-021).
package preparation

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/careerpilot/careerpilot/services/api/internal/aiclient"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
)

const (
	KindCV        = "CV"
	KindCover     = "COVER_LETTER"
	KindAnswers   = "APPLICATION_ANSWERS"
	SchemaVersion = "doc-v1"

	OriginGenerated = "GENERATED"
	OriginEdit      = "USER_EDIT"
	OriginRestored  = "RESTORED"

	NotValidated = "NOT_VALIDATED"

	maxSections   = 30
	maxBlocks     = 400
	maxBlockText  = 5000
	maxRefs       = 50
	maxQuestions  = 25
	maxQuestion   = 1000
	maxNotesChars = 4000
)

var validKinds = map[string]bool{KindCV: true, KindCover: true, KindAnswers: true}

var kindTitles = map[string]string{KindCV: "Tailored CV", KindCover: "Cover letter", KindAnswers: "Application answers"}

// Block, Section and Content mirror the AI service's doc-v1 model.
type Block struct {
	ID   string            `json:"id"`
	Type string            `json:"type"`
	Text string            `json:"text"`
	Refs []string          `json:"refs"`
	Meta map[string]string `json:"meta"`
}

type Section struct {
	Key    string  `json:"key"`
	Title  string  `json:"title"`
	Blocks []Block `json:"blocks"`
}

type Content struct {
	SchemaVersion string    `json:"schema_version"`
	Kind          string    `json:"kind"`
	Sections      []Section `json:"sections"`
}

var (
	blockTypes  = map[string]bool{"contact": true, "heading": true, "entry": true, "paragraph": true, "bullet": true, "skills": true, "answer": true}
	claimTypes  = map[string]bool{"paragraph": true, "bullet": true, "answer": true}
	idRe        = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)
	sectionKey  = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)
	editMetaKey = map[string]bool{"role": true, "parent": true, "status": true, "question_for_user": true}
	roles       = map[string]bool{"greeting": true, "closing": true}
)

// sanitize validates content submitted by a user (or returned by the AI
// service) and strips presentation metadata the platform renders itself.
func (c *Content) sanitize(kind string) error {
	errs := map[string]string{}
	if c.SchemaVersion != SchemaVersion {
		errs["schema_version"] = "Unsupported document schema."
	}
	if c.Kind != kind {
		errs["kind"] = "The content kind does not match the document."
	}
	if len(c.Sections) == 0 || len(c.Sections) > maxSections {
		errs["sections"] = fmt.Sprintf("Between 1 and %d sections.", maxSections)
	}
	ids := map[string]bool{}
	total := 0
	for si := range c.Sections {
		s := &c.Sections[si]
		s.Title = strings.TrimSpace(s.Title)
		if !sectionKey.MatchString(s.Key) {
			errs[fmt.Sprintf("sections.%d.key", si)] = "Invalid section key."
		}
		if utf8.RuneCountInString(s.Title) > maxQuestion {
			errs[fmt.Sprintf("sections.%d.title", si)] = "Title is too long."
		}
		for bi := range s.Blocks {
			b := &s.Blocks[bi]
			total++
			field := fmt.Sprintf("sections.%d.blocks.%d", si, bi)
			if !idRe.MatchString(b.ID) || ids[b.ID] {
				errs[field+".id"] = "Block ids must be unique."
			}
			ids[b.ID] = true
			if !blockTypes[b.Type] {
				errs[field+".type"] = "Unsupported block type."
			}
			b.Text = strings.TrimSpace(strings.ReplaceAll(b.Text, "\r\n", "\n"))
			if utf8.RuneCountInString(b.Text) > maxBlockText {
				errs[field+".text"] = fmt.Sprintf("Use at most %d characters.", maxBlockText)
			}
			if len(b.Refs) > maxRefs {
				errs[field+".refs"] = "Too many citations."
			}
			if b.Refs == nil {
				b.Refs = []string{}
			}
			meta := map[string]string{}
			for k, v := range b.Meta {
				if editMetaKey[k] && utf8.RuneCountInString(v) <= maxQuestion {
					meta[k] = v
				}
			}
			if r, ok := meta["role"]; ok && !roles[r] {
				delete(meta, "role")
			}
			// Writing an answer resolves its "needs input" state.
			if b.Text != "" {
				delete(meta, "status")
				delete(meta, "question_for_user")
			}
			b.Meta = meta
		}
	}
	if total > maxBlocks {
		errs["sections"] = fmt.Sprintf("At most %d blocks in total.", maxBlocks)
	}
	if len(errs) > 0 {
		return httpx.Validation(errs)
	}
	return nil
}

func (c Content) raw() json.RawMessage {
	b, _ := json.Marshal(c)
	return b
}

// Validation is the stored validation of a version (nil when not validated).
type Validation = aiclient.ValidationReport

type Document struct {
	ID                uuid.UUID  `json:"id"`
	JobID             uuid.UUID  `json:"job_id"`
	Kind              string     `json:"kind"`
	Title             string     `json:"title"`
	Notes             string     `json:"notes"`
	Questions         []string   `json:"questions"`
	CurrentVersionID  *uuid.UUID `json:"current_version_id"`
	ApprovedVersionID *uuid.UUID `json:"approved_version_id"`
	ApprovedAt        *time.Time `json:"approved_at"`
	// NONE | CURRENT (the current version is approved) | OUTDATED (an older one is).
	ApprovalState string    `json:"approval_state"`
	JobTitle      string    `json:"job_title"`
	Company       string    `json:"company"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (d *Document) derive() {
	d.Title = kindTitles[d.Kind]
	switch {
	case d.ApprovedVersionID == nil:
		d.ApprovalState = "NONE"
	case d.CurrentVersionID != nil && *d.CurrentVersionID == *d.ApprovedVersionID:
		d.ApprovalState = "CURRENT"
	default:
		d.ApprovalState = "OUTDATED"
	}
}

type VersionMeta struct {
	ID               uuid.UUID  `json:"id"`
	VersionNo        int        `json:"version_no"`
	Origin           string     `json:"origin"`
	RestoredFrom     *uuid.UUID `json:"restored_from"`
	ValidationStatus string     `json:"validation_status"`
	PromptVersion    *string    `json:"prompt_version"`
	Model            *string    `json:"model"`
	CreatedAt        time.Time  `json:"created_at"`
}

type Version struct {
	VersionMeta
	Content    Content     `json:"content"`
	Validation *Validation `json:"validation"`
	Questions  []string    `json:"questions"`
	Warnings   []string    `json:"warnings"`
	// FactsChanged is true when the profile changed after this version was
	// built or checked: re-check it before relying on the stored validation.
	FactsChanged bool `json:"facts_changed"`
}

type Generation struct {
	ID           uuid.UUID  `json:"id"`
	Status       string     `json:"status"`
	VersionID    *uuid.UUID `json:"version_id"`
	ErrorCode    *string    `json:"error_code"`
	ErrorMessage *string    `json:"error_message"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}
