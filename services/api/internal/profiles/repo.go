package profiles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
)

// All queries are scoped by user_id; a row owned by another user behaves
// exactly like a missing row (SECURITY_ARCHITECTURE §3).

var ErrNotFound = errors.New("not found")

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func affected(n int64, err error) error {
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- Basics ----

func getBasics(ctx context.Context, q db.Querier, userID uuid.UUID) (Basics, error) {
	var b Basics
	var links []byte
	err := q.QueryRow(ctx, `SELECT headline, summary, location_city, location_country, years_experience::float8, links
		FROM professional_profiles WHERE user_id = $1`, userID).
		Scan(&b.Headline, &b.Summary, &b.LocationCity, &b.LocationCountry, &b.YearsExperience, &links)
	if err != nil {
		return Basics{}, notFound(err)
	}
	if err := json.Unmarshal(links, &b.Links); err != nil {
		return Basics{}, fmt.Errorf("decode links: %w", err)
	}
	if b.Links == nil {
		b.Links = []Link{}
	}
	return b, nil
}

// upsertBasics writes the basics row (created at registration, but upsert
// keeps this robust).
func upsertBasics(ctx context.Context, q db.Querier, userID uuid.UUID, b Basics) error {
	links, err := json.Marshal(b.Links)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `
		INSERT INTO professional_profiles (user_id, headline, summary, location_city, location_country, years_experience, links)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (user_id) DO UPDATE SET headline = EXCLUDED.headline, summary = EXCLUDED.summary,
			location_city = EXCLUDED.location_city, location_country = EXCLUDED.location_country,
			years_experience = EXCLUDED.years_experience, links = EXCLUDED.links, updated_at = now()`,
		userID, b.Headline, b.Summary, b.LocationCity, b.LocationCountry, b.YearsExperience, links)
	return err
}

// ---- Experiences ----

func listExperiences(ctx context.Context, q db.Querier, userID uuid.UUID) ([]Experience, error) {
	rows, err := q.Query(ctx, `SELECT id, employer, title, location, start_date, end_date, is_current, description, achievements, source, verification
		FROM work_experiences WHERE user_id = $1 ORDER BY is_current DESC, start_date DESC NULLS LAST, created_at`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Experience, error) {
		var e Experience
		var start, end *time.Time
		var ach []byte
		err := r.Scan(&e.ID, &e.Employer, &e.Title, &e.Location, &start, &end, &e.IsCurrent, &e.Description, &ach, &e.Source, &e.Verification)
		if err == nil {
			e.StartDate, e.EndDate = dateFrom(start), dateFrom(end)
			err = json.Unmarshal(ach, &e.Achievements)
		}
		return e, err
	})
}

func insertExperience(ctx context.Context, q db.Querier, userID uuid.UUID, e Experience) error {
	ach, _ := json.Marshal(nonNil(e.Achievements))
	_, err := q.Exec(ctx, `INSERT INTO work_experiences (id, user_id, employer, title, location, start_date, end_date, is_current, description, achievements, source, verification)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		e.ID, userID, e.Employer, e.Title, e.Location, dateOrNil(e.StartDate), dateOrNil(e.EndDate), e.IsCurrent, e.Description, ach, e.Source, e.Verification)
	return err
}

func updateExperience(ctx context.Context, q db.Querier, userID uuid.UUID, e Experience) error {
	ach, _ := json.Marshal(nonNil(e.Achievements))
	tag, err := q.Exec(ctx, `UPDATE work_experiences SET employer=$3, title=$4, location=$5, start_date=$6, end_date=$7, is_current=$8,
		description=$9, achievements=$10, updated_at=now() WHERE id=$1 AND user_id=$2`,
		e.ID, userID, e.Employer, e.Title, e.Location, dateOrNil(e.StartDate), dateOrNil(e.EndDate), e.IsCurrent, e.Description, ach)
	return affected(tag.RowsAffected(), err)
}

// ---- Education ----

func listEducation(ctx context.Context, q db.Querier, userID uuid.UUID) ([]Education, error) {
	rows, err := q.Query(ctx, `SELECT id, institution, degree, field, start_date, end_date, source, verification
		FROM education_records WHERE user_id = $1 ORDER BY end_date DESC NULLS FIRST, created_at`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Education, error) {
		var e Education
		var start, end *time.Time
		err := r.Scan(&e.ID, &e.Institution, &e.Degree, &e.Field, &start, &end, &e.Source, &e.Verification)
		e.StartDate, e.EndDate = dateFrom(start), dateFrom(end)
		return e, err
	})
}

func insertEducation(ctx context.Context, q db.Querier, userID uuid.UUID, e Education) error {
	_, err := q.Exec(ctx, `INSERT INTO education_records (id, user_id, institution, degree, field, start_date, end_date, source, verification)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		e.ID, userID, e.Institution, e.Degree, e.Field, dateOrNil(e.StartDate), dateOrNil(e.EndDate), e.Source, e.Verification)
	return err
}

func updateEducation(ctx context.Context, q db.Querier, userID uuid.UUID, e Education) error {
	tag, err := q.Exec(ctx, `UPDATE education_records SET institution=$3, degree=$4, field=$5, start_date=$6, end_date=$7, updated_at=now()
		WHERE id=$1 AND user_id=$2`, e.ID, userID, e.Institution, e.Degree, e.Field, dateOrNil(e.StartDate), dateOrNil(e.EndDate))
	return affected(tag.RowsAffected(), err)
}

// ---- Skills ----

func listSkills(ctx context.Context, q db.Querier, userID uuid.UUID) ([]Skill, error) {
	rows, err := q.Query(ctx, `SELECT id, name, category, level, years::float8, source, verification
		FROM profile_skills WHERE user_id = $1 ORDER BY lower(name)`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Skill, error) {
		var s Skill
		err := r.Scan(&s.ID, &s.Name, &s.Category, &s.Level, &s.Years, &s.Source, &s.Verification)
		return s, err
	})
}

// insertSkill returns inserted=false when the user already has this skill.
func insertSkill(ctx context.Context, q db.Querier, userID uuid.UUID, s Skill) (bool, error) {
	tag, err := q.Exec(ctx, `INSERT INTO profile_skills (id, user_id, name, normalized_name, category, level, years, source, verification)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT (user_id, normalized_name) DO NOTHING`,
		s.ID, userID, s.Name, NormalizeSkill(s.Name), s.Category, s.Level, s.Years, s.Source, s.Verification)
	return tag.RowsAffected() == 1, err
}

func updateSkill(ctx context.Context, q db.Querier, userID uuid.UUID, s Skill) error {
	tag, err := q.Exec(ctx, `UPDATE profile_skills SET name=$3, normalized_name=$4, category=$5, level=$6, years=$7, updated_at=now()
		WHERE id=$1 AND user_id=$2`, s.ID, userID, s.Name, NormalizeSkill(s.Name), s.Category, s.Level, s.Years)
	return affected(tag.RowsAffected(), err)
}

// ---- Certifications ----

func listCertifications(ctx context.Context, q db.Querier, userID uuid.UUID) ([]Certification, error) {
	rows, err := q.Query(ctx, `SELECT id, name, issuer, issued_date, expires_date, credential_url, source, verification
		FROM certifications WHERE user_id = $1 ORDER BY issued_date DESC NULLS LAST, created_at`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Certification, error) {
		var c Certification
		var issued, expires *time.Time
		err := r.Scan(&c.ID, &c.Name, &c.Issuer, &issued, &expires, &c.CredentialURL, &c.Source, &c.Verification)
		c.IssuedDate, c.ExpiresDate = dateFrom(issued), dateFrom(expires)
		return c, err
	})
}

func insertCertification(ctx context.Context, q db.Querier, userID uuid.UUID, c Certification) error {
	_, err := q.Exec(ctx, `INSERT INTO certifications (id, user_id, name, issuer, issued_date, expires_date, credential_url, source, verification)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		c.ID, userID, c.Name, c.Issuer, dateOrNil(c.IssuedDate), dateOrNil(c.ExpiresDate), c.CredentialURL, c.Source, c.Verification)
	return err
}

func updateCertification(ctx context.Context, q db.Querier, userID uuid.UUID, c Certification) error {
	tag, err := q.Exec(ctx, `UPDATE certifications SET name=$3, issuer=$4, issued_date=$5, expires_date=$6, credential_url=$7, updated_at=now()
		WHERE id=$1 AND user_id=$2`, c.ID, userID, c.Name, c.Issuer, dateOrNil(c.IssuedDate), dateOrNil(c.ExpiresDate), c.CredentialURL)
	return affected(tag.RowsAffected(), err)
}

// ---- Projects ----

func listProjects(ctx context.Context, q db.Querier, userID uuid.UUID) ([]Project, error) {
	rows, err := q.Query(ctx, `SELECT id, name, description, url, source, verification
		FROM projects WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Project, error) {
		var p Project
		err := r.Scan(&p.ID, &p.Name, &p.Description, &p.URL, &p.Source, &p.Verification)
		return p, err
	})
}

func insertProject(ctx context.Context, q db.Querier, userID uuid.UUID, p Project) error {
	_, err := q.Exec(ctx, `INSERT INTO projects (id, user_id, name, description, url, source, verification) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		p.ID, userID, p.Name, p.Description, p.URL, p.Source, p.Verification)
	return err
}

func updateProject(ctx context.Context, q db.Querier, userID uuid.UUID, p Project) error {
	tag, err := q.Exec(ctx, `UPDATE projects SET name=$3, description=$4, url=$5, updated_at=now() WHERE id=$1 AND user_id=$2`,
		p.ID, userID, p.Name, p.Description, p.URL)
	return affected(tag.RowsAffected(), err)
}

// ---- Generic fact operations ----

// factTables maps API kinds to tables. Values are constants, never user input.
var factTables = map[string]string{
	"experiences":    "work_experiences",
	"education":      "education_records",
	"skills":         "profile_skills",
	"certifications": "certifications",
	"projects":       "projects",
}

func deleteFact(ctx context.Context, q db.Querier, kind string, userID, id uuid.UUID) error {
	table, ok := factTables[kind]
	if !ok {
		return ErrNotFound
	}
	tag, err := q.Exec(ctx, `DELETE FROM `+table+` WHERE id = $1 AND user_id = $2`, id, userID)
	return affected(tag.RowsAffected(), err)
}

func setVerification(ctx context.Context, q db.Querier, kind string, userID, id uuid.UUID, v Verification) error {
	table, ok := factTables[kind]
	if !ok {
		return ErrNotFound
	}
	tag, err := q.Exec(ctx, `UPDATE `+table+` SET verification = $3, updated_at = now() WHERE id = $1 AND user_id = $2`, id, userID, v)
	return affected(tag.RowsAffected(), err)
}

// ---- Preferences ----

func getPreferences(ctx context.Context, q db.Querier, userID uuid.UUID) (Preferences, error) {
	var p Preferences
	err := q.QueryRow(ctx, `SELECT target_roles, industries, employment_types, preferred_locations, remote_mode, work_eligible_countries,
		min_salary::float8, salary_currency, salary_period, needs_sponsorship, willing_to_relocate, notice_period_days, seniority_levels
		FROM user_preferences WHERE user_id = $1`, userID).
		Scan(&p.TargetRoles, &p.Industries, &p.EmploymentTypes, &p.PreferredLocations, &p.RemoteMode, &p.WorkEligibleCountries,
			&p.MinSalary, &p.SalaryCurrency, &p.SalaryPeriod, &p.NeedsSponsorship, &p.WillingToRelocate, &p.NoticePeriodDays, &p.SeniorityLevels)
	return p, notFound(err)
}

func putPreferences(ctx context.Context, q db.Querier, userID uuid.UUID, p Preferences) error {
	_, err := q.Exec(ctx, `
		INSERT INTO user_preferences (user_id, target_roles, industries, employment_types, preferred_locations, remote_mode, work_eligible_countries,
			min_salary, salary_currency, salary_period, needs_sponsorship, willing_to_relocate, notice_period_days, seniority_levels)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (user_id) DO UPDATE SET target_roles=EXCLUDED.target_roles, industries=EXCLUDED.industries,
			employment_types=EXCLUDED.employment_types, preferred_locations=EXCLUDED.preferred_locations, remote_mode=EXCLUDED.remote_mode,
			work_eligible_countries=EXCLUDED.work_eligible_countries, min_salary=EXCLUDED.min_salary, salary_currency=EXCLUDED.salary_currency,
			salary_period=EXCLUDED.salary_period, needs_sponsorship=EXCLUDED.needs_sponsorship, willing_to_relocate=EXCLUDED.willing_to_relocate,
			notice_period_days=EXCLUDED.notice_period_days, seniority_levels=EXCLUDED.seniority_levels, updated_at=now()`,
		userID, nonNil(p.TargetRoles), nonNil(p.Industries), nonNil(p.EmploymentTypes), nonNil(p.PreferredLocations), p.RemoteMode,
		nonNil(p.WorkEligibleCountries), p.MinSalary, p.SalaryCurrency, p.SalaryPeriod, p.NeedsSponsorship, p.WillingToRelocate,
		p.NoticePeriodDays, nonNil(p.SeniorityLevels))
	return err
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
