package matching

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/careerpilot/careerpilot/services/api/internal/aiclient"
)

func sp(s string) *string   { return &s }
func ip(i int) *int         { return &i }
func fp(f float64) *float64 { return &f }
func bp(b bool) *bool       { return &b }
func req(slug, name, imp string) aiclient.SkillRequirement {
	return aiclient.SkillRequirement{Slug: sp(slug), Name: name, Importance: imp, Evidence: name + " experience"}
}

func baseProfile() Profile {
	return Profile{
		Skills: []ProfileSkill{
			{Name: "Golang", Slug: "go", Verified: true},
			{Name: "PostgreSQL", Slug: "postgresql", Verified: false},
		},
		TextSkills:        []TextSkill{{Slug: "kafka", Name: "Kafka", Evidence: "built Kafka pipelines", Origin: "Backend Engineer at Acme", Verified: true}},
		YearsExperience:   fp(6),
		YearsSource:       "STATED",
		LocationCountry:   "KE",
		EligibleCountries: []string{"KE"},
		RemoteMode:        "REMOTE_ONLY",
		SeniorityLevels:   []string{"SENIOR"},
		TargetRoles:       []string{"Backend Engineer"},
		EmploymentTypes:   []string{"FULL_TIME"},
	}
}

func baseJob() Job {
	return Job{
		Title: "Senior Backend Engineer", Company: "Acme", LocationRaw: "Remote - Worldwide",
		RemoteStatus: "REMOTE", RemoteScope: "WORLDWIDE", EmploymentType: "FULL_TIME",
		Analysis: aiclient.JobAnalysis{
			Method: "DETERMINISTIC", AnalyzerVersion: "jd-det-1+tax-1",
			Skills: []aiclient.SkillRequirement{
				req("go", "Go", "REQUIRED"),
				req("postgresql", "PostgreSQL", "REQUIRED"),
				req("kafka", "Kafka", "REQUIRED"),
				req("rust", "Rust", "REQUIRED"),
				req("terraform", "Terraform", "PREFERRED"),
			},
			MinYears: ip(5), Seniority: "SENIOR", Sponsorship: "UNKNOWN", Degree: "NONE_STATED",
		},
		Similarity: fp(0.80),
	}
}

func check(r Result, key string) *Check {
	for i := range r.Checks {
		if r.Checks[i].Key == key {
			return &r.Checks[i]
		}
	}
	return nil
}

func dim(r Result, key string) Dimension {
	for _, d := range r.Dimensions {
		if d.Key == key {
			return d
		}
	}
	panic(key)
}

func TestEvaluateIsReproducible(t *testing.T) {
	p, j := baseProfile(), baseJob()
	a, _ := json.Marshal(Evaluate(p, j, DefaultWeights))
	for range 5 {
		b, _ := json.Marshal(Evaluate(p, j, DefaultWeights))
		if string(a) != string(b) {
			t.Fatalf("non-deterministic result:\n%s\n%s", a, b)
		}
	}
}

func TestSkillCreditsAndEvidence(t *testing.T) {
	r := Evaluate(baseProfile(), baseJob(), DefaultWeights)
	by := map[string]SkillMatch{}
	for _, m := range r.Skills {
		by[m.Name] = m
	}
	if m := by["Go"]; m.Status != "MATCHED" || m.ProfileSource != "SKILL_VERIFIED" || m.Credit != 1 {
		t.Fatalf("go: %+v", m)
	}
	if m := by["PostgreSQL"]; m.Status != "PARTIAL" || m.ProfileSource != "SKILL_UNVERIFIED" || m.Credit != creditSkillUnverified {
		t.Fatalf("postgres: %+v", m)
	}
	if m := by["Kafka"]; m.Status != "PARTIAL" || m.ProfileSource != "EXPERIENCE_VERIFIED" || !strings.Contains(m.ProfileEvidence, "Acme") {
		t.Fatalf("kafka: %+v", m)
	}
	if strings.Join(r.MissingSkills, ",") != "Rust" || strings.Join(r.MissingPreferred, ",") != "Terraform" {
		t.Fatalf("missing: %v / %v", r.MissingSkills, r.MissingPreferred)
	}
	// (3·1 + 3·0.6 + 3·0.5 + 3·0 + 1·0) / 13 = 6.3 / 13 = 0.4846
	if got := *dim(r, "skills").Score; got != 48.5 {
		t.Fatalf("skills score = %v", got)
	}
	if !strings.Contains(strings.Join(r.Explanation, " "), "Go (confirmed on your profile)") {
		t.Fatalf("explanation lacks evidence: %v", r.Explanation)
	}
}

func TestUnknownsLowerConfidenceNotScore(t *testing.T) {
	p, j := baseProfile(), baseJob()
	full := Evaluate(p, j, DefaultWeights)

	j2 := baseJob()
	j2.Analysis.MinYears = nil
	j2.Analysis.Seniority = "UNKNOWN"
	j2.EmploymentType = "UNKNOWN"
	j2.Similarity = nil
	p2 := baseProfile()
	p2.TargetRoles = nil
	r := Evaluate(p2, j2, DefaultWeights)
	if dim(r, "experience").Score != nil || dim(r, "domain").Score != nil {
		t.Fatal("unknown dimensions must have nil score")
	}
	if r.KnownWeight >= full.KnownWeight {
		t.Fatalf("known weight %v should drop below %v", r.KnownWeight, full.KnownWeight)
	}
	if r.Confidence == "HIGH" {
		t.Fatalf("confidence %s", r.Confidence)
	}
	// Score is the weighted mean of known dimensions only.
	var num, den float64
	for _, d := range r.Dimensions {
		if d.Score != nil {
			num += d.Weight * *d.Score
			den += d.Weight
		}
	}
	if r.Score != round1(num/den) {
		t.Fatalf("score %v != mean of known %v", r.Score, round1(num/den))
	}
	if len(r.Unknowns) == 0 {
		t.Fatal("unknowns must be listed")
	}
}

func TestMissingInformationNeverRejects(t *testing.T) {
	j := baseJob()
	j.RemoteStatus, j.RemoteScope = "UNKNOWN", "UNKNOWN"
	j.Analysis.DegreeRequired, j.Analysis.Degree = bp(true), "BACHELORS"
	p := baseProfile()
	p.NeedsSponsorship = bp(true)
	p.MinSalary, p.SalaryCurrency, p.SalaryPeriod = fp(90000), "USD", "YEAR"
	r := Evaluate(p, j, DefaultWeights)
	if r.Eligibility != Unknown {
		t.Fatalf("eligibility = %s, want UNKNOWN (no rejection on missing info)", r.Eligibility)
	}
	for _, k := range []string{"REMOTE_REQUIRED", "SPONSORSHIP", "SALARY_FLOOR", "QUALIFICATION"} {
		if c := check(r, k); c == nil || c.Status != CheckUnknown {
			t.Fatalf("%s: %+v", k, c)
		}
	}
	if len(r.Blockers) != 0 {
		t.Fatalf("blockers: %v", r.Blockers)
	}
}

func TestHardEligibilityFailures(t *testing.T) {
	cases := map[string]func(*Profile, *Job){
		"onsite for remote-only": func(p *Profile, j *Job) {
			j.RemoteStatus, j.LocationCountries = "ONSITE", []string{"KE"}
		},
		"remote countries exclude user": func(p *Profile, j *Job) {
			j.RemoteScope, j.RemoteCountries = "COUNTRIES", []string{"US", "CA"}
		},
		"region excludes user": func(p *Profile, j *Job) {
			j.RemoteScope, j.RemoteRegion = "REGION", "North America"
		},
		"salary below floor": func(p *Profile, j *Job) {
			p.MinSalary, p.SalaryCurrency, p.SalaryPeriod = fp(10000), "USD", "MONTH"
			j.SalaryAnnualMin, j.SalaryAnnualMax, j.SalaryCurrency = fp(80000), fp(100000), "USD"
		},
		"sponsorship refused": func(p *Profile, j *Job) {
			p.NeedsSponsorship = bp(true)
			j.RemoteScope, j.RemoteCountries = "COUNTRIES", []string{"US", "KE"}
			p.EligibleCountries, p.LocationCountry = []string{"GB"}, "GB"
			j.RemoteCountries = []string{"GB", "US"}
			j.Analysis.Sponsorship = "NOT_AVAILABLE"
		},
		"work authorisation required": func(p *Profile, j *Job) {
			j.Analysis.WorkAuthorization = []string{"US"}
			j.Analysis.WorkAuthorizationEvidence = sp("must be authorized to work in the United States")
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p, j := baseProfile(), baseJob()
			mutate(&p, &j)
			r := Evaluate(p, j, DefaultWeights)
			if r.Eligibility != Ineligible || len(r.Blockers) == 0 {
				t.Fatalf("eligibility %s blockers %v checks %+v", r.Eligibility, r.Blockers, r.Checks)
			}
			if !strings.HasPrefix(explainLine(r, "Not eligible"), "Not eligible: ") {
				t.Fatalf("explanation: %v", r.Explanation)
			}
		})
	}
}

func explainLine(r Result, prefix string) string {
	for _, l := range r.Explanation {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	return ""
}

func TestEligibilityPassesAndAmbiguity(t *testing.T) {
	p, j := baseProfile(), baseJob()
	j.RemoteScope, j.RemoteRegion = "REGION", "EMEA"
	if r := Evaluate(p, j, DefaultWeights); check(r, "LOCATION_ELIGIBILITY").Status != CheckPass || r.Eligibility != Eligible {
		t.Fatalf("EMEA should include KE: %+v", r.Checks)
	}
	p.EligibleCountries, p.LocationCountry = []string{"MX"}, "MX"
	j.RemoteRegion = "North America"
	if r := Evaluate(p, j, DefaultWeights); check(r, "LOCATION_ELIGIBILITY").Status != CheckUnknown {
		t.Fatalf("MX in North America is ambiguous: %+v", r.Checks)
	}
	// Different currency: never compared.
	p2, j2 := baseProfile(), baseJob()
	p2.MinSalary, p2.SalaryCurrency, p2.SalaryPeriod = fp(5000000), "KES", "YEAR"
	j2.SalaryAnnualMax, j2.SalaryCurrency = fp(20000), "USD"
	if c := check(Evaluate(p2, j2, DefaultWeights), "SALARY_FLOOR"); c.Status != CheckUnknown || !strings.Contains(c.Detail, "not converted") {
		t.Fatalf("currency mismatch: %+v", c)
	}
	// Sponsorship is irrelevant when the job is inside the user's eligible countries.
	p3, j3 := baseProfile(), baseJob()
	p3.NeedsSponsorship = bp(true)
	j3.RemoteScope, j3.RemoteCountries = "COUNTRIES", []string{"KE"}
	j3.Analysis.Sponsorship = "NOT_AVAILABLE"
	if r := Evaluate(p3, j3, DefaultWeights); check(r, "SPONSORSHIP") != nil || r.Eligibility != Eligible {
		t.Fatalf("sponsorship should not apply: %+v", r.Checks)
	}
}

func TestEmptyProfileSkillsAreUnknownNotZero(t *testing.T) {
	p := baseProfile()
	p.Skills, p.TextSkills = nil, nil
	r := Evaluate(p, baseJob(), DefaultWeights)
	if dim(r, "skills").Score != nil || r.Confidence != "LOW" {
		t.Fatalf("skills %v confidence %s", dim(r, "skills").Score, r.Confidence)
	}
	for _, m := range r.Skills {
		if m.Status != "UNKNOWN" {
			t.Fatalf("status %s", m.Status)
		}
	}
	if len(r.MissingSkills) != 0 {
		t.Fatalf("cannot claim missing skills for an empty profile: %v", r.MissingSkills)
	}
}

func TestExperienceAndSeniority(t *testing.T) {
	p, j := baseProfile(), baseJob()
	p.YearsExperience = fp(2)
	j.Analysis.MinYears = ip(5) // shortfall 3/5 → 1 - 0.9 = 0.1
	j.Analysis.Seniority = "STAFF"
	r := Evaluate(p, j, DefaultWeights)
	d := dim(r, "experience")
	if *d.Components[0].Score != 10 || *d.Components[1].Score != 50 {
		t.Fatalf("components %+v %+v", *d.Components[0].Score, *d.Components[1].Score)
	}
	if *d.Score != round1(10*0.6+50*0.4) {
		t.Fatalf("experience = %v", *d.Score)
	}
}

func TestDegreeNeverFailsInV1(t *testing.T) {
	p, j := baseProfile(), baseJob()
	j.Analysis.Degree, j.Analysis.DegreeRequired = "DOCTORATE", bp(true)
	p.HasEducation, p.HighestDegree = true, "BACHELORS"
	if c := check(Evaluate(p, j, DefaultWeights), "QUALIFICATION"); c.Status != CheckUnknown {
		t.Fatalf("%+v", c)
	}
	p.HighestDegree = "DOCTORATE"
	if c := check(Evaluate(p, j, DefaultWeights), "QUALIFICATION"); c.Status != CheckPass {
		t.Fatalf("%+v", c)
	}
}

func TestWeightsAreConfigurableAndRecorded(t *testing.T) {
	p, j := baseProfile(), baseJob()
	a := Evaluate(p, j, DefaultWeights)
	w := Weights{Skills: 100}
	b := Evaluate(p, j, w)
	if b.Score != *dim(b, "skills").Score || b.Weights != w || a.Score == b.Score {
		t.Fatalf("weights not applied: %v vs %v", a.Score, b.Score)
	}
}

func TestLLMAnalysisAllowsHighConfidence(t *testing.T) {
	p, j := baseProfile(), baseJob()
	p.MinSalary, p.SalaryCurrency, p.SalaryPeriod = fp(100000), "USD", "YEAR"
	j.SalaryAnnualMin, j.SalaryAnnualMax, j.SalaryCurrency = fp(120000), fp(150000), "USD"
	if r := Evaluate(p, j, DefaultWeights); r.Confidence != "MEDIUM" || r.KnownWeight != 100 {
		t.Fatalf("deterministic: %s %v", r.Confidence, r.KnownWeight)
	}
	j.Analysis.Method = "LLM"
	if r := Evaluate(p, j, DefaultWeights); r.Confidence != "HIGH" {
		t.Fatalf("llm: %s", r.Confidence)
	}
}

func TestTitleTokens(t *testing.T) {
	got := strings.Join(titleTokens("Sr. Back End Developer II (Remote)"), " ")
	if got != "back-end engineer" {
		t.Fatalf("%q", got)
	}
}
