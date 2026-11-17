// Package matching evaluates jobs against a user's profile (ADR-010, ADR-019).
//
// Evaluate is a pure function: the same Profile, Job and Weights always
// produce the same Result. Hard eligibility checks are kept separate from the
// weighted suitability score. Unknown inputs are reported as unknown: they
// lower confidence, they do not lower the score, and they never cause an
// automatic rejection.
package matching

import (
	"github.com/google/uuid"

	"github.com/careerpilot/careerpilot/services/api/internal/aiclient"
)

// AlgorithmVersion identifies the scoring rules below. Bump it whenever a rule,
// constant or weight default changes; results are stored per version.
const AlgorithmVersion = "match-v1"

// Weights are percentages of the overall score (master prompt §7).
type Weights struct {
	Skills       float64 `json:"skills"`
	Experience   float64 `json:"experience"`
	Domain       float64 `json:"domain"`
	Location     float64 `json:"location"`
	Compensation float64 `json:"compensation"`
}

var DefaultWeights = Weights{Skills: 35, Experience: 20, Domain: 15, Location: 15, Compensation: 15}

// ---- Inputs ----

type ProfileSkill struct {
	Name     string `json:"name"`
	Slug     string `json:"slug,omitempty"` // canonical; empty if not catalogued
	Verified bool   `json:"verified"`
}

// TextSkill is a catalogue skill found in an experience or project description.
type TextSkill struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Evidence string `json:"evidence"`
	Origin   string `json:"origin"` // e.g. "Backend Engineer at Acme"
	Verified bool   `json:"verified"`
}

type Profile struct {
	Skills            []ProfileSkill `json:"skills"`
	TextSkills        []TextSkill    `json:"text_skills"`
	YearsExperience   *float64       `json:"years_experience"`
	YearsSource       string         `json:"years_source"` // STATED | COMPUTED | ""
	HighestDegree     string         `json:"highest_degree"`
	HasEducation      bool           `json:"has_education"`
	LocationCountry   string         `json:"location_country"`
	EligibleCountries []string       `json:"eligible_countries"`
	RemoteMode        string         `json:"remote_mode"`
	NeedsSponsorship  *bool          `json:"needs_sponsorship"`
	WillingToRelocate *bool          `json:"willing_to_relocate"`
	MinSalary         *float64       `json:"min_salary"`
	SalaryCurrency    string         `json:"salary_currency"`
	SalaryPeriod      string         `json:"salary_period"`
	EmploymentTypes   []string       `json:"employment_types"`
	SeniorityLevels   []string       `json:"seniority_levels"`
	TargetRoles       []string       `json:"target_roles"`
	EmbeddingModel    string         `json:"embedding_model"`
}

type Job struct {
	ID                uuid.UUID            `json:"id"`
	Title             string               `json:"title"`
	Company           string               `json:"company"`
	LocationRaw       string               `json:"location_raw"`
	LocationCountries []string             `json:"location_countries"`
	RemoteStatus      string               `json:"remote_status"`
	RemoteScope       string               `json:"remote_scope"`
	RemoteCountries   []string             `json:"remote_countries"`
	RemoteRegion      string               `json:"remote_region"`
	EmploymentType    string               `json:"employment_type"`
	SalaryAnnualMin   *float64             `json:"salary_annual_min"`
	SalaryAnnualMax   *float64             `json:"salary_annual_max"`
	SalaryCurrency    string               `json:"salary_currency"`
	ContentHash       []byte               `json:"content_hash"`
	Analysis          aiclient.JobAnalysis `json:"analysis"`
	EmbeddingModel    string               `json:"embedding_model"`
	// Similarity is the cosine similarity between the profile and job
	// embeddings (same model), or nil when either is missing.
	Similarity *float64 `json:"similarity"`
}

// ---- Output ----

const (
	CheckPass    = "PASS"
	CheckFail    = "FAIL"
	CheckUnknown = "UNKNOWN"

	Eligible   = "ELIGIBLE"
	Ineligible = "INELIGIBLE"
	Unknown    = "UNKNOWN"
)

type Component struct {
	Key    string   `json:"key"`
	Label  string   `json:"label"`
	Score  *float64 `json:"score"` // 0..100, nil = unknown
	Detail string   `json:"detail"`
}

type Dimension struct {
	Key        string      `json:"key"`
	Label      string      `json:"label"`
	Weight     float64     `json:"weight"`
	Score      *float64    `json:"score"` // 0..100, nil = unknown
	Summary    string      `json:"summary"`
	Components []Component `json:"components"`
}

type Check struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Status   string `json:"status"` // PASS | FAIL | UNKNOWN
	Critical bool   `json:"critical"`
	Detail   string `json:"detail"`
	Evidence string `json:"evidence,omitempty"` // verbatim from the posting
}

type SkillMatch struct {
	Name            string  `json:"name"`
	Slug            string  `json:"slug,omitempty"`
	Importance      string  `json:"importance"`
	Weight          float64 `json:"weight"`
	Status          string  `json:"status"` // MATCHED | PARTIAL | MISSING
	Credit          float64 `json:"credit"`
	JobEvidence     string  `json:"job_evidence"`
	ProfileSource   string  `json:"profile_source,omitempty"` // SKILL_VERIFIED | SKILL_UNVERIFIED | EXPERIENCE_VERIFIED | EXPERIENCE_UNVERIFIED
	ProfileEvidence string  `json:"profile_evidence,omitempty"`
}

type Signal struct {
	Kind string `json:"kind"` // POSITIVE | NEGATIVE
	Text string `json:"text"`
}

type Result struct {
	AlgorithmVersion string       `json:"algorithm_version"`
	Score            float64      `json:"score"`
	Eligibility      string       `json:"eligibility"`
	Confidence       string       `json:"confidence"`
	KnownWeight      float64      `json:"known_weight"` // share of weight with known inputs, 0..100
	AnalysisMethod   string       `json:"analysis_method"`
	AnalyzerVersion  string       `json:"analyzer_version"`
	Dimensions       []Dimension  `json:"dimensions"`
	Checks           []Check      `json:"checks"`
	Skills           []SkillMatch `json:"skills"`
	MissingSkills    []string     `json:"missing_skills"`
	MissingPreferred []string     `json:"missing_preferred"`
	Blockers         []string     `json:"blockers"`
	Signals          []Signal     `json:"signals"`
	Unknowns         []string     `json:"unknowns"`
	Explanation      []string     `json:"explanation"`
	Weights          Weights      `json:"weights"`
}
