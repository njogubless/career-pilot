package matching

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/careerpilot/careerpilot/services/api/internal/profiles"
)

// profileInputs is the profile data the matching pipeline reads, plus what
// the AI service derived from it.
type profileInputs struct {
	Profile       Profile
	EmbeddingText string
	// Texts sent for skill-mention detection, aligned with TextOrigins.
	Texts       []string
	TextOrigins []textOrigin
}

type textOrigin struct {
	Label    string
	Verified bool
}

func usable(p profiles.Provenance) bool { return p.Verification != profiles.Rejected }

func verified(p profiles.Provenance) bool {
	return p.Verification == profiles.Verified
}

// buildInputs maps the profile aggregate onto matching inputs. Rejected facts
// are excluded; unverified facts are kept but flagged so they earn less credit.
// slugs maps profile skill ids to canonical slugs; now fixes computed years.
func buildInputs(pr profiles.Profile, slugs map[string]string, now time.Time) profileInputs {
	var in profileInputs
	p := &in.Profile
	p.Skills = []ProfileSkill{}
	p.TextSkills = []TextSkill{}
	for _, s := range pr.Skills {
		if !usable(s.Provenance) {
			continue
		}
		p.Skills = append(p.Skills, ProfileSkill{Name: s.Name, Slug: slugs[s.ID.String()], Verified: verified(s.Provenance)})
	}
	slices.SortFunc(p.Skills, func(a, b ProfileSkill) int { return strings.Compare(normName(a.Name), normName(b.Name)) })

	b := pr.Basics
	if b.YearsExperience != nil {
		p.YearsExperience, p.YearsSource = b.YearsExperience, "STATED"
	} else if y := computedYears(pr.Experiences, now); y != nil {
		p.YearsExperience, p.YearsSource = y, "COMPUTED"
	}
	if b.LocationCountry != nil {
		p.LocationCountry = *b.LocationCountry
	}
	for _, e := range pr.Education {
		if !usable(e.Provenance) {
			continue
		}
		p.HasEducation = true
		if lv := degreeLevel(e.Degree); degreeRank[lv] > degreeRank[p.HighestDegree] {
			p.HighestDegree = lv
		}
	}

	pf := pr.Preferences
	p.EligibleCountries = sortedCopy(pf.WorkEligibleCountries)
	p.RemoteMode = pf.RemoteMode
	p.NeedsSponsorship = pf.NeedsSponsorship
	p.WillingToRelocate = pf.WillingToRelocate
	p.MinSalary = pf.MinSalary
	if pf.SalaryCurrency != nil {
		p.SalaryCurrency = *pf.SalaryCurrency
	}
	if pf.SalaryPeriod != nil {
		p.SalaryPeriod = *pf.SalaryPeriod
	}
	p.EmploymentTypes = sortedCopy(pf.EmploymentTypes)
	p.SeniorityLevels = sortedCopy(pf.SeniorityLevels)
	p.TargetRoles = slices.Clone(pf.TargetRoles)

	// Embedding text: who the person is professionally, most recent first.
	var parts []string
	if b.Headline != "" {
		parts = append(parts, b.Headline)
	}
	if len(pf.TargetRoles) > 0 {
		parts = append(parts, "Target roles: "+strings.Join(pf.TargetRoles, ", "))
	}
	if b.Summary != "" {
		parts = append(parts, b.Summary)
	}
	exps := slices.Clone(pr.Experiences)
	slices.SortStableFunc(exps, func(a, b profiles.Experience) int { return startOf(b).Compare(startOf(a)) })
	for _, e := range exps {
		if !usable(e.Provenance) {
			continue
		}
		label := e.Title + " at " + e.Employer
		parts = append(parts, strings.TrimSpace(label+". "+e.Description+" "+strings.Join(e.Achievements, " ")))
		text := strings.TrimSpace(e.Title + "\n" + e.Description + "\n" + strings.Join(e.Achievements, "\n"))
		in.Texts = append(in.Texts, text)
		in.TextOrigins = append(in.TextOrigins, textOrigin{Label: label, Verified: verified(e.Provenance)})
	}
	for _, pj := range pr.Projects {
		if !usable(pj.Provenance) {
			continue
		}
		parts = append(parts, pj.Name+". "+pj.Description)
		in.Texts = append(in.Texts, pj.Name+"\n"+pj.Description)
		in.TextOrigins = append(in.TextOrigins, textOrigin{Label: "Project " + pj.Name, Verified: verified(pj.Provenance)})
	}
	if len(p.Skills) > 0 {
		names := make([]string, len(p.Skills))
		for i, s := range p.Skills {
			names[i] = s.Name
		}
		parts = append(parts, "Skills: "+strings.Join(names, ", "))
	}
	in.EmbeddingText = strings.TrimSpace(strings.Join(parts, "\n"))
	if r := []rune(in.EmbeddingText); len(r) > 2000 {
		in.EmbeddingText = string(r[:2000]) // the model reads at most 512 tokens
	}
	return in
}

func startOf(e profiles.Experience) time.Time {
	if e.StartDate == nil {
		return time.Time{}
	}
	return e.StartDate.Time
}

// computedYears is the length of the union of dated, non-rejected roles, in
// whole months, rounded down to half years. Undated roles are ignored.
func computedYears(exps []profiles.Experience, now time.Time) *float64 {
	type span struct{ from, to time.Time }
	var spans []span
	for _, e := range exps {
		if !usable(e.Provenance) || e.StartDate == nil {
			continue
		}
		end := now
		if e.EndDate != nil {
			end = e.EndDate.Time
		} else if !e.IsCurrent {
			continue
		}
		if end.After(e.StartDate.Time) {
			spans = append(spans, span{e.StartDate.Time, end})
		}
	}
	if len(spans) == 0 {
		return nil
	}
	slices.SortFunc(spans, func(a, b span) int { return a.from.Compare(b.from) })
	months := 0
	cur := spans[0]
	flush := func(s span) {
		months += (s.to.Year()-s.from.Year())*12 + int(s.to.Month()) - int(s.from.Month())
	}
	for _, s := range spans[1:] {
		if !s.from.After(cur.to) {
			if s.to.After(cur.to) {
				cur.to = s.to
			}
			continue
		}
		flush(cur)
		cur = s
	}
	flush(cur)
	y := math.Floor(float64(months)/6) / 2
	return &y
}

var (
	reDoctorate = regexp.MustCompile(`(?i)\b(ph\.?\s?d|doctor(ate)?|d\.?phil|ed\.?d)\b`)
	reMasters   = regexp.MustCompile(`(?i)\b(master'?s?|m\.?sc|m\.?eng|mba|m\.?phil|m\.?a|m\.?s|llm)\b`)
	reBachelors = regexp.MustCompile(`(?i)\b(bachelor'?s?|b\.?sc|b\.?eng|b\.?tech|b\.?a|b\.?s|b\.?com|bba|llb|undergraduate)\b`)
)

// degreeLevel classifies a free-text degree name; "" when unclear.
func degreeLevel(s string) string {
	switch {
	case reDoctorate.MatchString(s):
		return "DOCTORATE"
	case reMasters.MatchString(s):
		return "MASTERS"
	case reBachelors.MatchString(s):
		return "BACHELORS"
	}
	return ""
}

func sortedCopy(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	if out == nil {
		out = []string{}
	}
	return out
}

func hashOf(parts ...any) string {
	h := sha256.New()
	enc := json.NewEncoder(h)
	for _, p := range parts {
		_ = enc.Encode(p)
	}
	return hex.EncodeToString(h.Sum(nil))
}
