package applications

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
)

const (
	kindCV      = "CV"
	kindCover   = "COVER_LETTER"
	kindAnswers = "APPLICATION_ANSWERS"
)

// PackageDoc is one tailored document for the application's job (Phase 5).
type PackageDoc struct {
	DocumentID        uuid.UUID  `json:"document_id"`
	Kind              string     `json:"kind"`
	ApprovalState     string     `json:"approval_state"` // NONE | CURRENT | OUTDATED
	ApprovedVersionID *uuid.UUID `json:"approved_version_id"`
	ApprovedVersionNo *int       `json:"approved_version_no"`
	CurrentVersionNo  *int       `json:"current_version_no"`
	ApprovedAt        *time.Time `json:"approved_at"`
}

// Package is what an approval would pin: the approved version of each
// document. A CV is required; other documents are included when approved.
type Package struct {
	Documents []PackageDoc `json:"documents"`
	Ready     bool         `json:"ready"`
	Blockers  []string     `json:"blockers"`
	Warnings  []string     `json:"warnings"`
}

var kindNames = map[string]string{kindCV: "CV", kindCover: "Cover letter", kindAnswers: "Application answers"}

func loadPackage(ctx context.Context, q db.Querier, userID, jobID uuid.UUID) (Package, error) {
	rows, err := q.Query(ctx, `SELECT d.id, d.kind, d.approved_version_id, av.version_no, cv.version_no, d.approved_at,
			coalesce(d.current_version_id = d.approved_version_id, false)
		FROM application_documents d
		LEFT JOIN application_document_versions av ON av.id = d.approved_version_id
		LEFT JOIN application_document_versions cv ON cv.id = d.current_version_id
		WHERE d.user_id = $1 AND d.job_id = $2
		ORDER BY array_position(ARRAY['CV','COVER_LETTER','APPLICATION_ANSWERS'], d.kind)`, userID, jobID)
	if err != nil {
		return Package{}, err
	}
	defer rows.Close()
	p := Package{Documents: []PackageDoc{}, Blockers: []string{}, Warnings: []string{}}
	for rows.Next() {
		var d PackageDoc
		var current bool
		if err := rows.Scan(&d.DocumentID, &d.Kind, &d.ApprovedVersionID, &d.ApprovedVersionNo, &d.CurrentVersionNo, &d.ApprovedAt, &current); err != nil {
			return Package{}, err
		}
		switch {
		case d.ApprovedVersionID == nil:
			d.ApprovalState = "NONE"
		case current:
			d.ApprovalState = "CURRENT"
		default:
			d.ApprovalState = "OUTDATED"
		}
		p.Documents = append(p.Documents, d)
	}
	if err := rows.Err(); err != nil {
		return Package{}, err
	}
	hasCV := false
	for _, d := range p.Documents {
		name := kindNames[d.Kind]
		switch d.ApprovalState {
		case "NONE":
			if d.Kind == kindCV {
				p.Blockers = append(p.Blockers, "Approve a version of the tailored CV first.")
			} else {
				p.Warnings = append(p.Warnings, name+" is not approved and will not be included.")
			}
		case "OUTDATED":
			p.Warnings = append(p.Warnings, name+" has edits after the approved version; the approved version is used.")
		}
		if d.Kind == kindCV && d.ApprovedVersionID != nil {
			hasCV = true
		}
	}
	if !hasCV && len(p.Blockers) == 0 {
		p.Blockers = append(p.Blockers, "Prepare and approve a tailored CV for this job first.")
	}
	p.Ready = hasCV
	return p, nil
}

// pins returns the approved version per kind (nil when not included).
func (p Package) pins() map[string]*uuid.UUID {
	out := map[string]*uuid.UUID{kindCV: nil, kindCover: nil, kindAnswers: nil}
	for _, d := range p.Documents {
		if d.ApprovedVersionID != nil {
			out[d.Kind] = d.ApprovedVersionID
		}
	}
	return out
}
