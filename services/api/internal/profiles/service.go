package profiles

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/careerpilot/careerpilot/services/api/internal/audit"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
)

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func mapErr(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return httpx.NewError(httpx.CodeNotFound, "Resource not found.")
	case db.IsCheckViolation(err):
		return &httpx.Error{Code: httpx.CodeValidationFailed, Message: "One or more values are out of range.", Cause: err}
	}
	return err
}

func validationErr(v validator) error {
	if len(v) == 0 {
		return nil
	}
	return httpx.Validation(v)
}

// Get loads the full profile aggregate with completeness.
func (s *Service) Get(ctx context.Context, userID uuid.UUID) (Profile, error) {
	var p Profile
	var err error
	if p.Basics, err = getBasics(ctx, s.pool, userID); err != nil {
		return p, mapErr(err)
	}
	if p.Experiences, err = listExperiences(ctx, s.pool, userID); err != nil {
		return p, err
	}
	if p.Education, err = listEducation(ctx, s.pool, userID); err != nil {
		return p, err
	}
	if p.Skills, err = listSkills(ctx, s.pool, userID); err != nil {
		return p, err
	}
	if p.Certifications, err = listCertifications(ctx, s.pool, userID); err != nil {
		return p, err
	}
	if p.Projects, err = listProjects(ctx, s.pool, userID); err != nil {
		return p, err
	}
	if p.Preferences, err = getPreferences(ctx, s.pool, userID); err != nil {
		return p, mapErr(err)
	}
	p.Completeness = computeCompleteness(p)
	return p, nil
}

func (s *Service) Completeness(ctx context.Context, userID uuid.UUID) (Completeness, error) {
	p, err := s.Get(ctx, userID)
	return p.Completeness, err
}

func (s *Service) UpdateBasics(ctx context.Context, userID uuid.UUID, b Basics) (Basics, error) {
	v := validator{}
	b.validate(v)
	if err := validationErr(v); err != nil {
		return Basics{}, err
	}
	if err := upsertBasics(ctx, s.pool, userID, b); err != nil {
		return Basics{}, mapErr(err)
	}
	return getBasics(ctx, s.pool, userID)
}

func (s *Service) UpdatePreferences(ctx context.Context, userID uuid.UUID, p Preferences) (Preferences, error) {
	v := validator{}
	p.validate(v)
	if err := validationErr(v); err != nil {
		return Preferences{}, err
	}
	if err := putPreferences(ctx, s.pool, userID, p); err != nil {
		return Preferences{}, mapErr(err)
	}
	return getPreferences(ctx, s.pool, userID)
}

// User-entered facts are verified by definition: the user is the source.
var userEntered = Provenance{Source: SourceUser, Verification: Verified}

func (s *Service) CreateExperience(ctx context.Context, userID uuid.UUID, e Experience) (Experience, error) {
	v := validator{}
	e.validate(v)
	if err := validationErr(v); err != nil {
		return e, err
	}
	e.ID, e.Provenance = ids.New(), userEntered
	return e, mapErr(insertExperience(ctx, s.pool, userID, e))
}

func (s *Service) UpdateExperience(ctx context.Context, userID uuid.UUID, e Experience) error {
	v := validator{}
	e.validate(v)
	if err := validationErr(v); err != nil {
		return err
	}
	return mapErr(updateExperience(ctx, s.pool, userID, e))
}

func (s *Service) CreateEducation(ctx context.Context, userID uuid.UUID, e Education) (Education, error) {
	v := validator{}
	e.validate(v)
	if err := validationErr(v); err != nil {
		return e, err
	}
	e.ID, e.Provenance = ids.New(), userEntered
	return e, mapErr(insertEducation(ctx, s.pool, userID, e))
}

func (s *Service) UpdateEducation(ctx context.Context, userID uuid.UUID, e Education) error {
	v := validator{}
	e.validate(v)
	if err := validationErr(v); err != nil {
		return err
	}
	return mapErr(updateEducation(ctx, s.pool, userID, e))
}

func (s *Service) CreateSkill(ctx context.Context, userID uuid.UUID, sk Skill) (Skill, error) {
	v := validator{}
	sk.validate(v)
	if err := validationErr(v); err != nil {
		return sk, err
	}
	sk.ID, sk.Provenance = ids.New(), userEntered
	inserted, err := insertSkill(ctx, s.pool, userID, sk)
	if err != nil {
		return sk, mapErr(err)
	}
	if !inserted {
		return sk, &httpx.Error{Code: httpx.CodeConflict, Message: "This skill is already on your profile.",
			Details: map[string]string{"name": "Already added."}}
	}
	return sk, nil
}

func (s *Service) UpdateSkill(ctx context.Context, userID uuid.UUID, sk Skill) error {
	v := validator{}
	sk.validate(v)
	if err := validationErr(v); err != nil {
		return err
	}
	err := updateSkill(ctx, s.pool, userID, sk)
	if db.IsUniqueViolation(err, "") {
		return &httpx.Error{Code: httpx.CodeConflict, Message: "This skill is already on your profile.", Details: map[string]string{"name": "Already added."}}
	}
	return mapErr(err)
}

func (s *Service) CreateCertification(ctx context.Context, userID uuid.UUID, c Certification) (Certification, error) {
	v := validator{}
	c.validate(v)
	if err := validationErr(v); err != nil {
		return c, err
	}
	c.ID, c.Provenance = ids.New(), userEntered
	return c, mapErr(insertCertification(ctx, s.pool, userID, c))
}

func (s *Service) UpdateCertification(ctx context.Context, userID uuid.UUID, c Certification) error {
	v := validator{}
	c.validate(v)
	if err := validationErr(v); err != nil {
		return err
	}
	return mapErr(updateCertification(ctx, s.pool, userID, c))
}

func (s *Service) CreateProject(ctx context.Context, userID uuid.UUID, p Project) (Project, error) {
	v := validator{}
	p.validate(v)
	if err := validationErr(v); err != nil {
		return p, err
	}
	p.ID, p.Provenance = ids.New(), userEntered
	return p, mapErr(insertProject(ctx, s.pool, userID, p))
}

func (s *Service) UpdateProject(ctx context.Context, userID uuid.UUID, p Project) error {
	v := validator{}
	p.validate(v)
	if err := validationErr(v); err != nil {
		return err
	}
	return mapErr(updateProject(ctx, s.pool, userID, p))
}

func (s *Service) DeleteFact(ctx context.Context, userID uuid.UUID, kind string, id uuid.UUID) error {
	return mapErr(deleteFact(ctx, s.pool, kind, userID, id))
}

// SetVerification is the only path that marks an imported or inferred fact
// as VERIFIED, and it requires an explicit user request. It is audited.
func (s *Service) SetVerification(ctx context.Context, userID uuid.UUID, kind string, id uuid.UUID, v Verification) error {
	if !v.valid() {
		return httpx.Validation(map[string]string{"verification": "Use VERIFIED, UNVERIFIED or REJECTED."})
	}
	return mapErr(db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := setVerification(ctx, tx, kind, userID, id, v); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "profile.fact.verification", SubjectType: kind,
			SubjectID: &id, Outcome: audit.Success, Metadata: map[string]any{"verification": v}})
	}))
}

// ---- Import from a reviewed CV extraction ----

// ImportItem wraps a reviewed item with the user's explicit verification choice.
type ImportItem[T any] struct {
	Item     T    `json:"item"`
	Verified bool `json:"verified"`
}

// ImportBatch is what the user accepted while reviewing an extraction.
type ImportBatch struct {
	Basics         *Basics                     `json:"basics"`
	Experiences    []ImportItem[Experience]    `json:"experiences"`
	Education      []ImportItem[Education]     `json:"education"`
	Skills         []ImportItem[Skill]         `json:"skills"`
	Certifications []ImportItem[Certification] `json:"certifications"`
	Projects       []ImportItem[Project]       `json:"projects"`
}

type ImportResult struct {
	Created       int `json:"created"`
	SkippedSkills int `json:"skipped_duplicate_skills"`
	Verified      int `json:"verified"`
}

func importProv(verified bool) Provenance {
	if verified {
		return Provenance{Source: SourceCV, Verification: Verified}
	}
	return Provenance{Source: SourceCV, Verification: Unverified}
}

// Import validates and writes accepted items inside the caller's transaction.
// Items keep source CV_IMPORT; they are VERIFIED only when the user ticked it.
func Import(ctx context.Context, tx pgx.Tx, userID uuid.UUID, b ImportBatch) (ImportResult, error) {
	v := validator{}
	var res ImportResult
	count := func(verified bool) {
		res.Created++
		if verified {
			res.Verified++
		}
	}
	if b.Basics != nil {
		bv := validator{}
		b.Basics.validate(bv)
		for k, m := range bv {
			v["basics."+k] = m
		}
	}
	for i := range b.Experiences {
		ev := validator{}
		b.Experiences[i].Item.validate(ev)
		prefix(v, ev, fmt.Sprintf("experiences.%d.", i))
	}
	for i := range b.Education {
		ev := validator{}
		b.Education[i].Item.validate(ev)
		prefix(v, ev, fmt.Sprintf("education.%d.", i))
	}
	for i := range b.Skills {
		ev := validator{}
		b.Skills[i].Item.validate(ev)
		prefix(v, ev, fmt.Sprintf("skills.%d.", i))
	}
	for i := range b.Certifications {
		ev := validator{}
		b.Certifications[i].Item.validate(ev)
		prefix(v, ev, fmt.Sprintf("certifications.%d.", i))
	}
	for i := range b.Projects {
		ev := validator{}
		b.Projects[i].Item.validate(ev)
		prefix(v, ev, fmt.Sprintf("projects.%d.", i))
	}
	if err := validationErr(v); err != nil {
		return res, err
	}

	if b.Basics != nil {
		if err := upsertBasics(ctx, tx, userID, *b.Basics); err != nil {
			return res, mapErr(err)
		}
	}
	for _, it := range b.Experiences {
		e := it.Item
		e.ID, e.Provenance = ids.New(), importProv(it.Verified)
		if err := insertExperience(ctx, tx, userID, e); err != nil {
			return res, mapErr(err)
		}
		count(it.Verified)
	}
	for _, it := range b.Education {
		e := it.Item
		e.ID, e.Provenance = ids.New(), importProv(it.Verified)
		if err := insertEducation(ctx, tx, userID, e); err != nil {
			return res, mapErr(err)
		}
		count(it.Verified)
	}
	for _, it := range b.Skills {
		sk := it.Item
		sk.ID, sk.Provenance = ids.New(), importProv(it.Verified)
		inserted, err := insertSkill(ctx, tx, userID, sk)
		if err != nil {
			return res, mapErr(err)
		}
		if !inserted {
			res.SkippedSkills++
			continue
		}
		count(it.Verified)
	}
	for _, it := range b.Certifications {
		c := it.Item
		c.ID, c.Provenance = ids.New(), importProv(it.Verified)
		if err := insertCertification(ctx, tx, userID, c); err != nil {
			return res, mapErr(err)
		}
		count(it.Verified)
	}
	for _, it := range b.Projects {
		p := it.Item
		p.ID, p.Provenance = ids.New(), importProv(it.Verified)
		if err := insertProject(ctx, tx, userID, p); err != nil {
			return res, mapErr(err)
		}
		count(it.Verified)
	}
	return res, nil
}

func prefix(dst, src validator, p string) {
	for k, m := range src {
		dst[p+k] = m
	}
}
