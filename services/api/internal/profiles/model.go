// Package profiles manages a user's professional profile: facts with
// provenance (source + verification), career preferences and completeness.
package profiles

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Source and Verification implement the fact provenance model
// (MASTER_BUILD_PROMPT §5, DATABASE_DESIGN §1).
type Source string

const (
	SourceUser Source = "USER_ENTERED"
	SourceCV   Source = "CV_IMPORT"
	SourceAI   Source = "AI_INFERRED"
)

type Verification string

const (
	Verified   Verification = "VERIFIED"
	Unverified Verification = "UNVERIFIED"
	Rejected   Verification = "REJECTED"
)

func (v Verification) valid() bool { return v == Verified || v == Unverified || v == Rejected }

type Provenance struct {
	Source       Source       `json:"source"`
	Verification Verification `json:"verification"`
}

// Date is a calendar date serialised as YYYY-MM-DD.
type Date struct{ time.Time }

func (d Date) MarshalJSON() ([]byte, error) { return json.Marshal(d.Format(time.DateOnly)) }

func (d *Date) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("date must be a string")
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return fmt.Errorf("date must be YYYY-MM-DD")
	}
	d.Time = t
	return nil
}

func dateOrNil(d *Date) *time.Time {
	if d == nil {
		return nil
	}
	return &d.Time
}

func dateFrom(t *time.Time) *Date {
	if t == nil {
		return nil
	}
	return &Date{*t}
}

type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type Basics struct {
	Headline        string   `json:"headline"`
	Summary         string   `json:"summary"`
	LocationCity    string   `json:"location_city"`
	LocationCountry *string  `json:"location_country"`
	YearsExperience *float64 `json:"years_experience"`
	Links           []Link   `json:"links"`
}

type Experience struct {
	ID           uuid.UUID `json:"id"`
	Employer     string    `json:"employer"`
	Title        string    `json:"title"`
	Location     string    `json:"location"`
	StartDate    *Date     `json:"start_date"`
	EndDate      *Date     `json:"end_date"`
	IsCurrent    bool      `json:"is_current"`
	Description  string    `json:"description"`
	Achievements []string  `json:"achievements"`
	Provenance
}

type Education struct {
	ID          uuid.UUID `json:"id"`
	Institution string    `json:"institution"`
	Degree      string    `json:"degree"`
	Field       string    `json:"field"`
	StartDate   *Date     `json:"start_date"`
	EndDate     *Date     `json:"end_date"`
	Provenance
}

type Skill struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Category string    `json:"category"`
	Level    *string   `json:"level"`
	Years    *float64  `json:"years"`
	Provenance
}

type Certification struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	Issuer        string    `json:"issuer"`
	IssuedDate    *Date     `json:"issued_date"`
	ExpiresDate   *Date     `json:"expires_date"`
	CredentialURL string    `json:"credential_url"`
	Provenance
}

type Project struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	URL         string    `json:"url"`
	Provenance
}

type Preferences struct {
	TargetRoles           []string `json:"target_roles"`
	Industries            []string `json:"industries"`
	EmploymentTypes       []string `json:"employment_types"`
	PreferredLocations    []string `json:"preferred_locations"`
	RemoteMode            string   `json:"remote_mode"`
	WorkEligibleCountries []string `json:"work_eligible_countries"`
	MinSalary             *float64 `json:"min_salary"`
	SalaryCurrency        *string  `json:"salary_currency"`
	SalaryPeriod          *string  `json:"salary_period"`
	NeedsSponsorship      *bool    `json:"needs_sponsorship"`
	WillingToRelocate     *bool    `json:"willing_to_relocate"`
	NoticePeriodDays      *int     `json:"notice_period_days"`
	SeniorityLevels       []string `json:"seniority_levels"`
}

// Profile is the aggregate returned by GET /profiles/me.
type Profile struct {
	Basics         Basics          `json:"basics"`
	Experiences    []Experience    `json:"experiences"`
	Education      []Education     `json:"education"`
	Skills         []Skill         `json:"skills"`
	Certifications []Certification `json:"certifications"`
	Projects       []Project       `json:"projects"`
	Preferences    Preferences     `json:"preferences"`
	Completeness   Completeness    `json:"completeness"`
}

// ---- Validation helpers ----

type validator map[string]string

func (v validator) text(field, val string, min, max int) string {
	val = strings.TrimSpace(val)
	n := utf8.RuneCountInString(val)
	if n < min {
		v[field] = "Required."
	} else if n > max {
		v[field] = fmt.Sprintf("Use at most %d characters.", max)
	}
	return val
}

// httpURL accepts empty or absolute http(s) URLs only, preventing
// javascript:/data: links from being stored and rendered.
func (v validator) httpURL(field, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(raw) > 500 {
		v[field] = "Enter a full http(s) URL."
	}
	return raw
}

func (v validator) dateRange(field string, start, end *Date) {
	if start != nil && end != nil && end.Before(start.Time) {
		v[field] = "End date must be after the start date."
	}
}

var (
	countryRe  = regexp.MustCompile(`^[A-Z]{2}$`)
	currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)
)

func (v validator) stringList(field string, in []string, maxItems, maxLen int) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[strings.ToLower(s)] {
			continue
		}
		if utf8.RuneCountInString(s) > maxLen {
			v[field] = fmt.Sprintf("Each entry must be at most %d characters.", maxLen)
		}
		seen[strings.ToLower(s)] = true
		out = append(out, s)
	}
	if len(out) > maxItems {
		v[field] = fmt.Sprintf("At most %d entries.", maxItems)
	}
	return out
}

// NormalizeSkill produces the de-duplication key for a skill name.
func NormalizeSkill(name string) string {
	return strings.Join(strings.Fields(strings.ToLower(name)), " ")
}

func (e *Experience) validate(v validator) {
	e.Employer = v.text("employer", e.Employer, 1, 200)
	e.Title = v.text("title", e.Title, 1, 200)
	e.Location = v.text("location", e.Location, 0, 200)
	e.Description = v.text("description", e.Description, 0, 5000)
	e.Achievements = v.stringList("achievements", e.Achievements, 30, 1000)
	if e.IsCurrent {
		e.EndDate = nil
	}
	v.dateRange("end_date", e.StartDate, e.EndDate)
}

func (e *Education) validate(v validator) {
	e.Institution = v.text("institution", e.Institution, 1, 200)
	e.Degree = v.text("degree", e.Degree, 0, 200)
	e.Field = v.text("field", e.Field, 0, 200)
	v.dateRange("end_date", e.StartDate, e.EndDate)
}

var skillLevels = map[string]bool{"BEGINNER": true, "INTERMEDIATE": true, "ADVANCED": true, "EXPERT": true}

func (s *Skill) validate(v validator) {
	s.Name = v.text("name", s.Name, 1, 100)
	s.Category = v.text("category", s.Category, 0, 100)
	if s.Level != nil && !skillLevels[*s.Level] {
		v["level"] = "Choose a valid level."
	}
	if s.Years != nil && (*s.Years < 0 || *s.Years > 60) {
		v["years"] = "Years must be between 0 and 60."
	}
}

func (c *Certification) validate(v validator) {
	c.Name = v.text("name", c.Name, 1, 200)
	c.Issuer = v.text("issuer", c.Issuer, 0, 200)
	c.CredentialURL = v.httpURL("credential_url", c.CredentialURL)
	v.dateRange("expires_date", c.IssuedDate, c.ExpiresDate)
}

func (p *Project) validate(v validator) {
	p.Name = v.text("name", p.Name, 1, 200)
	p.Description = v.text("description", p.Description, 0, 5000)
	p.URL = v.httpURL("url", p.URL)
}

func (b *Basics) validate(v validator) {
	b.Headline = v.text("headline", b.Headline, 0, 200)
	b.Summary = v.text("summary", b.Summary, 0, 5000)
	b.LocationCity = v.text("location_city", b.LocationCity, 0, 120)
	if b.LocationCountry != nil {
		c := strings.ToUpper(strings.TrimSpace(*b.LocationCountry))
		if c == "" {
			b.LocationCountry = nil
		} else if !countryRe.MatchString(c) {
			v["location_country"] = "Use a two-letter ISO country code."
		} else {
			b.LocationCountry = &c
		}
	}
	if b.YearsExperience != nil && (*b.YearsExperience < 0 || *b.YearsExperience > 80) {
		v["years_experience"] = "Years must be between 0 and 80."
	}
	if len(b.Links) > 20 {
		v["links"] = "At most 20 links."
	}
	links := make([]Link, 0, len(b.Links))
	for i, l := range b.Links {
		l.Label = v.text(fmt.Sprintf("links.%d.label", i), l.Label, 1, 60)
		l.URL = v.httpURL(fmt.Sprintf("links.%d.url", i), l.URL)
		if l.URL == "" {
			v[fmt.Sprintf("links.%d.url", i)] = "Required."
		}
		links = append(links, l)
	}
	b.Links = links
}

var (
	remoteModes     = map[string]bool{"REMOTE_ONLY": true, "HYBRID_OK": true, "ONSITE_OK": true}
	salaryPeriods   = map[string]bool{"YEAR": true, "MONTH": true, "HOUR": true}
	employmentTypes = map[string]bool{"FULL_TIME": true, "PART_TIME": true, "CONTRACT": true, "INTERNSHIP": true, "TEMPORARY": true}
	seniorities     = map[string]bool{"INTERN": true, "JUNIOR": true, "MID": true, "SENIOR": true, "STAFF": true, "PRINCIPAL": true, "LEAD": true, "MANAGER": true, "DIRECTOR": true, "EXECUTIVE": true}
)

func (p *Preferences) validate(v validator) {
	p.TargetRoles = v.stringList("target_roles", p.TargetRoles, 20, 100)
	p.Industries = v.stringList("industries", p.Industries, 20, 100)
	p.PreferredLocations = v.stringList("preferred_locations", p.PreferredLocations, 20, 120)
	p.EmploymentTypes = v.stringList("employment_types", p.EmploymentTypes, 5, 20)
	for _, t := range p.EmploymentTypes {
		if !employmentTypes[t] {
			v["employment_types"] = "Contains an unsupported employment type."
		}
	}
	p.SeniorityLevels = v.stringList("seniority_levels", p.SeniorityLevels, 10, 20)
	for _, s := range p.SeniorityLevels {
		if !seniorities[s] {
			v["seniority_levels"] = "Contains an unsupported seniority level."
		}
	}
	if !remoteModes[p.RemoteMode] {
		v["remote_mode"] = "Choose a remote-work preference."
	}
	countries := v.stringList("work_eligible_countries", p.WorkEligibleCountries, 60, 2)
	for i, c := range countries {
		countries[i] = strings.ToUpper(c)
		if !countryRe.MatchString(countries[i]) {
			v["work_eligible_countries"] = "Use two-letter ISO country codes."
		}
	}
	p.WorkEligibleCountries = countries
	set := 0
	if p.MinSalary != nil {
		set++
		if *p.MinSalary < 0 || *p.MinSalary > 1e10 {
			v["min_salary"] = "Enter a valid amount."
		}
	}
	if p.SalaryCurrency != nil {
		set++
		c := strings.ToUpper(*p.SalaryCurrency)
		p.SalaryCurrency = &c
		if !currencyRe.MatchString(c) {
			v["salary_currency"] = "Use a three-letter currency code."
		}
	}
	if p.SalaryPeriod != nil {
		set++
		if !salaryPeriods[*p.SalaryPeriod] {
			v["salary_period"] = "Choose year, month or hour."
		}
	}
	if set != 0 && set != 3 {
		v["min_salary"] = "Provide amount, currency and period together, or leave all empty."
	}
	if p.NoticePeriodDays != nil && (*p.NoticePeriodDays < 0 || *p.NoticePeriodDays > 365) {
		v["notice_period_days"] = "Enter between 0 and 365 days."
	}
}
