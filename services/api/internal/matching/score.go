package matching

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

// Scoring constants (part of AlgorithmVersion).
const (
	weightRequired  = 3.0
	weightMentioned = 1.5
	weightPreferred = 1.0

	creditSkillVerified   = 1.0 // listed on the profile and verified by the user
	creditSkillUnverified = 0.6 // listed but not yet confirmed (e.g. CV import)
	creditTextVerified    = 0.5 // only mentioned in a verified experience/project
	creditTextUnverified  = 0.3

	// Cosine similarity mapped linearly onto 0..100 between these bounds
	// (calibrated for bge-small-en-v1.5 profile↔posting pairs).
	simLow  = 0.60
	simHigh = 0.85

	yearsShortfallPenalty = 1.5 // 1 - 1.5 × relative shortfall
)

var seniorityRank = map[string]int{
	"INTERN": 0, "JUNIOR": 1, "MID": 2, "SENIOR": 3, "LEAD": 4, "STAFF": 4, "MANAGER": 4,
	"PRINCIPAL": 5, "DIRECTOR": 6, "EXECUTIVE": 7,
}

var degreeRank = map[string]int{"NONE_STATED": 0, "BACHELORS": 1, "MASTERS": 2, "DOCTORATE": 3}

var degreeLabel = map[string]string{"BACHELORS": "bachelor's degree", "MASTERS": "master's degree", "DOCTORATE": "doctorate"}

// Evaluate scores one job for one profile. It is pure and deterministic.
func Evaluate(p Profile, j Job, w Weights) Result {
	r := Result{
		AlgorithmVersion: AlgorithmVersion,
		AnalysisMethod:   j.Analysis.Method,
		AnalyzerVersion:  j.Analysis.AnalyzerVersion,
		Weights:          w,
		MissingSkills:    []string{},
		MissingPreferred: []string{},
		Blockers:         []string{},
		Signals:          []Signal{},
		Unknowns:         []string{},
	}
	eligible := eligibleCountries(p)
	r.Checks = evaluateChecks(p, j, eligible)

	skills, matches := scoreSkills(p, j, w.Skills)
	r.Skills = matches
	for _, m := range matches {
		if m.Status == "MISSING" {
			if m.Importance == "PREFERRED" {
				r.MissingPreferred = append(r.MissingPreferred, m.Name)
			} else {
				r.MissingSkills = append(r.MissingSkills, m.Name)
			}
		}
	}
	r.Dimensions = []Dimension{
		skills,
		scoreExperience(p, j, w.Experience),
		scoreDomain(p, j, w.Domain),
		scoreLocation(p, j, w.Location, r.Checks),
		scoreCompensation(p, j, w.Compensation, r.Checks),
	}

	var total, known, all float64
	for _, d := range r.Dimensions {
		all += d.Weight
		if d.Score != nil {
			known += d.Weight
			total += d.Weight * *d.Score
		}
	}
	if known > 0 {
		r.Score = round1(total / known)
	}
	if all > 0 {
		r.KnownWeight = round1(known / all * 100)
	}

	r.Eligibility = Eligible
	for _, c := range r.Checks {
		switch {
		case c.Status == CheckFail:
			r.Eligibility = Ineligible
			r.Blockers = append(r.Blockers, c.Detail)
		case c.Status == CheckUnknown:
			r.Unknowns = append(r.Unknowns, c.Detail)
			if c.Critical && r.Eligibility == Eligible {
				r.Eligibility = Unknown
			}
		}
	}
	for _, d := range r.Dimensions {
		for _, comp := range d.Components {
			if comp.Score == nil {
				r.Unknowns = append(r.Unknowns, comp.Detail)
			}
		}
	}
	r.Unknowns = dedupe(r.Unknowns)

	switch {
	case r.KnownWeight >= 85:
		r.Confidence = "HIGH"
	case r.KnownWeight >= 60:
		r.Confidence = "MEDIUM"
	default:
		r.Confidence = "LOW"
	}
	// Heuristic requirement extraction cannot fully separate required from
	// preferred skills, so it never supports HIGH confidence.
	if r.Confidence == "HIGH" && j.Analysis.Method != "LLM" {
		r.Confidence = "MEDIUM"
	}
	if skills.Score == nil {
		r.Confidence = "LOW"
	}

	r.Signals = buildSignals(r, j)
	r.Explanation = explain(r)
	return r
}

// ---- Hard eligibility checks ----

func eligibleCountries(p Profile) []string {
	out := slices.Clone(p.EligibleCountries)
	if p.LocationCountry != "" && !slices.Contains(out, p.LocationCountry) {
		out = append(out, p.LocationCountry)
	}
	slices.Sort(out)
	return out
}

func evaluateChecks(p Profile, j Job, eligible []string) []Check {
	var out []Check
	a := j.Analysis

	if p.RemoteMode == "REMOTE_ONLY" {
		c := Check{Key: "REMOTE_REQUIRED", Label: "Remote work", Critical: true, Evidence: j.LocationRaw}
		switch j.RemoteStatus {
		case "REMOTE":
			c.Status, c.Detail = CheckPass, "The role is remote."
		case "ONSITE", "HYBRID":
			c.Status, c.Detail = CheckFail, fmt.Sprintf("The role is %s, and you only accept remote roles.", strings.ToLower(j.RemoteStatus))
		default:
			c.Status, c.Detail = CheckUnknown, "The posting does not say whether the role is remote."
		}
		out = append(out, c)
	}

	out = append(out, locationCheck(j, eligible))

	if len(a.WorkAuthorization) > 0 {
		c := Check{Key: "WORK_AUTHORIZATION", Label: "Work authorisation", Evidence: deref(a.WorkAuthorizationEvidence)}
		req := strings.Join(a.WorkAuthorization, ", ")
		switch {
		case len(eligible) == 0:
			c.Status, c.Detail = CheckUnknown, fmt.Sprintf("The posting requires authorisation to work in %s. Add your work-eligible countries.", req)
		case overlaps(a.WorkAuthorization, eligible):
			c.Status, c.Detail = CheckPass, fmt.Sprintf("You can work in %s, as the posting requires.", strings.Join(intersect(a.WorkAuthorization, eligible), ", "))
		default:
			c.Status, c.Detail = CheckFail, fmt.Sprintf("The posting requires existing authorisation to work in %s.", req)
		}
		out = append(out, c)
	}

	if p.NeedsSponsorship != nil && *p.NeedsSponsorship {
		jobCountries := union(j.LocationCountries, j.RemoteCountries)
		if !(len(eligible) > 0 && len(jobCountries) > 0 && subset(jobCountries, eligible)) {
			c := Check{Key: "SPONSORSHIP", Label: "Visa sponsorship", Evidence: deref(a.SponsorshipEvidence)}
			switch a.Sponsorship {
			case "AVAILABLE":
				c.Status, c.Detail = CheckPass, "The posting says visa sponsorship is available."
			case "NOT_AVAILABLE":
				c.Status, c.Detail = CheckFail, "You need visa sponsorship, and the posting says it is not available."
			default:
				c.Status, c.Detail = CheckUnknown, "The posting does not say whether it sponsors visas."
			}
			out = append(out, c)
		}
	}

	if p.MinSalary != nil {
		c := Check{Key: "SALARY_FLOOR", Label: "Minimum salary"}
		floor := annualize(*p.MinSalary, p.SalaryPeriod)
		top := maxPtr(j.SalaryAnnualMin, j.SalaryAnnualMax)
		switch {
		case top == nil:
			c.Status, c.Detail = CheckUnknown, "The posting does not disclose a salary."
		case !strings.EqualFold(j.SalaryCurrency, p.SalaryCurrency):
			c.Status, c.Detail = CheckUnknown, fmt.Sprintf("The salary is in %s and your minimum is in %s. Currencies are not converted.", j.SalaryCurrency, p.SalaryCurrency)
		case *top < floor:
			c.Status, c.Detail = CheckFail, fmt.Sprintf("The highest disclosed salary (%s %s a year) is below your minimum (%s %s).", money(*top), j.SalaryCurrency, money(floor), p.SalaryCurrency)
		default:
			c.Status, c.Detail = CheckPass, fmt.Sprintf("The disclosed salary reaches your minimum of %s %s a year.", money(floor), p.SalaryCurrency)
		}
		out = append(out, c)
	}

	if a.DegreeRequired != nil && *a.DegreeRequired && degreeRank[a.Degree] > 0 {
		c := Check{Key: "QUALIFICATION", Label: "Required qualification", Evidence: deref(a.DegreeEvidence)}
		need := degreeLabel[a.Degree]
		switch {
		case degreeRank[p.HighestDegree] >= degreeRank[a.Degree]:
			c.Status, c.Detail = CheckPass, fmt.Sprintf("The posting requires a %s; your profile includes one.", need)
		case !p.HasEducation:
			c.Status, c.Detail = CheckUnknown, fmt.Sprintf("The posting requires a %s; your profile has no education entries.", need)
		default:
			// v1 never rejects on qualifications: degree names are too varied to
			// parse reliably, so a mismatch is surfaced for the user to judge.
			c.Status, c.Detail = CheckUnknown, fmt.Sprintf("The posting requires a %s; your profile does not clearly show one.", need)
		}
		out = append(out, c)
	}
	return out
}

func locationCheck(j Job, eligible []string) Check {
	c := Check{Key: "LOCATION_ELIGIBILITY", Label: "Location eligibility", Critical: true, Evidence: j.LocationRaw}
	if len(eligible) == 0 {
		c.Status, c.Detail = CheckUnknown, "Add the countries where you can work to your preferences."
		return c
	}
	mine := strings.Join(eligible, ", ")
	switch j.RemoteStatus {
	case "REMOTE":
		switch j.RemoteScope {
		case "WORLDWIDE":
			c.Status, c.Detail = CheckPass, "Open to remote candidates worldwide."
		case "COUNTRIES":
			if overlaps(j.RemoteCountries, eligible) {
				c.Status, c.Detail = CheckPass, fmt.Sprintf("Open to remote candidates in %s.", strings.Join(intersect(j.RemoteCountries, eligible), ", "))
			} else {
				c.Status, c.Detail = CheckFail, fmt.Sprintf("Remote only for candidates in %s; your eligible countries are %s.", strings.Join(j.RemoteCountries, ", "), mine)
			}
		case "REGION":
			switch regionStatus(j.RemoteRegion, eligible) {
			case CheckPass:
				c.Status, c.Detail = CheckPass, fmt.Sprintf("Open to remote candidates in %s, which includes your eligible countries.", j.RemoteRegion)
			case CheckFail:
				c.Status, c.Detail = CheckFail, fmt.Sprintf("Remote only within %s, which does not include %s.", j.RemoteRegion, mine)
			default:
				c.Status, c.Detail = CheckUnknown, fmt.Sprintf("Remote within %s; whether that includes %s is unclear.", j.RemoteRegion, mine)
			}
		default:
			c.Status, c.Detail = CheckUnknown, "The posting does not say where remote candidates may live."
		}
	case "ONSITE", "HYBRID":
		switch {
		case len(j.LocationCountries) == 0:
			c.Status, c.Detail = CheckUnknown, "The office country could not be determined."
		case overlaps(j.LocationCountries, eligible):
			c.Status, c.Detail = CheckPass, fmt.Sprintf("The office is in %s, where you can work.", strings.Join(intersect(j.LocationCountries, eligible), ", "))
		case j.Analysis.Sponsorship == "NOT_AVAILABLE":
			c.Status, c.Detail = CheckFail, fmt.Sprintf("The office is in %s, which is not among your eligible countries, and the posting says it cannot sponsor visas.", strings.Join(j.LocationCountries, ", "))
		case j.Analysis.Sponsorship == "AVAILABLE":
			c.Status, c.Detail = CheckUnknown, fmt.Sprintf("The office is in %s, which is not among your eligible countries; the posting offers visa sponsorship.", strings.Join(j.LocationCountries, ", "))
		default:
			c.Status, c.Detail = CheckUnknown, fmt.Sprintf("The office is in %s, which is not among your eligible countries; the posting does not mention sponsorship.", strings.Join(j.LocationCountries, ", "))
		}
	default:
		if overlaps(j.LocationCountries, eligible) {
			c.Status, c.Detail = CheckPass, fmt.Sprintf("The role is based in %s, where you can work.", strings.Join(intersect(j.LocationCountries, eligible), ", "))
		} else {
			c.Status, c.Detail = CheckUnknown, "The posting does not state the work arrangement or location clearly."
		}
	}
	return c
}

// ---- Weighted dimensions ----

func scoreSkills(p Profile, j Job, weight float64) (Dimension, []SkillMatch) {
	d := Dimension{Key: "skills", Label: "Skills", Weight: weight, Components: []Component{}}
	reqs := j.Analysis.Skills
	matches := make([]SkillMatch, 0, len(reqs))
	if len(reqs) == 0 {
		d.Summary = "No skill requirements were identified in the posting."
		d.Components = append(d.Components, Component{Key: "requirements", Label: "Skill requirements", Detail: "The posting's skill requirements could not be identified."})
		return d, matches
	}
	profileEmpty := len(p.Skills) == 0 && len(p.TextSkills) == 0

	bySlug := map[string]ProfileSkill{}
	byName := map[string]ProfileSkill{}
	for _, s := range p.Skills {
		if s.Slug != "" {
			if prev, ok := bySlug[s.Slug]; !ok || (!prev.Verified && s.Verified) {
				bySlug[s.Slug] = s
			}
		}
		key := normName(s.Name)
		if prev, ok := byName[key]; !ok || (!prev.Verified && s.Verified) {
			byName[key] = s
		}
	}
	text := map[string]TextSkill{}
	for _, t := range p.TextSkills {
		if prev, ok := text[t.Slug]; !ok || (!prev.Verified && t.Verified) {
			text[t.Slug] = t
		}
	}

	var num, den float64
	var reqTotal, reqMet, prefTotal, prefMet int
	for _, req := range reqs {
		m := SkillMatch{Name: req.Name, Slug: deref(req.Slug), Importance: req.Importance, JobEvidence: req.Evidence}
		switch req.Importance {
		case "REQUIRED":
			m.Weight = weightRequired
		case "PREFERRED":
			m.Weight = weightPreferred
		default:
			m.Weight = weightMentioned
		}
		ps, ok := bySlug[m.Slug]
		if !ok || m.Slug == "" {
			ps, ok = byName[normName(req.Name)]
		}
		switch {
		case ok && ps.Verified:
			m.Credit, m.ProfileSource, m.ProfileEvidence = creditSkillVerified, "SKILL_VERIFIED", "Listed on your profile as "+ps.Name
		case ok:
			m.Credit, m.ProfileSource, m.ProfileEvidence = creditSkillUnverified, "SKILL_UNVERIFIED", "Listed on your profile as "+ps.Name+" (not yet confirmed)"
		default:
			if t, ok := text[m.Slug]; ok && m.Slug != "" {
				m.Credit = creditTextUnverified
				m.ProfileSource = "EXPERIENCE_UNVERIFIED"
				if t.Verified {
					m.Credit, m.ProfileSource = creditTextVerified, "EXPERIENCE_VERIFIED"
				}
				m.ProfileEvidence = t.Origin + ": “" + t.Evidence + "”"
			}
		}
		switch {
		case profileEmpty:
			m.Status = "UNKNOWN"
		case m.Credit >= 0.99:
			m.Status = "MATCHED"
		case m.Credit > 0:
			m.Status = "PARTIAL"
		default:
			m.Status = "MISSING"
		}
		num += m.Weight * m.Credit
		den += m.Weight
		if req.Importance == "PREFERRED" {
			prefTotal++
			if m.Credit > 0 {
				prefMet++
			}
		} else {
			reqTotal++
			if m.Credit > 0 {
				reqMet++
			}
		}
		matches = append(matches, m)
	}
	if profileEmpty {
		d.Summary = "Add skills to your profile to compare them with this posting."
		d.Components = append(d.Components, Component{Key: "profile", Label: "Your skills", Detail: "Your profile lists no skills yet."})
		return d, matches
	}
	d.Score = ptr(round1(num / den * 100))
	if reqTotal > 0 {
		d.Components = append(d.Components, Component{Key: "required", Label: "Core skills", Score: ptr(round1(float64(reqMet) / float64(reqTotal) * 100)),
			Detail: fmt.Sprintf("%d of %d core skills evidenced on your profile.", reqMet, reqTotal)})
	}
	if prefTotal > 0 {
		d.Components = append(d.Components, Component{Key: "preferred", Label: "Nice to have", Score: ptr(round1(float64(prefMet) / float64(prefTotal) * 100)),
			Detail: fmt.Sprintf("%d of %d nice-to-have skills evidenced.", prefMet, prefTotal)})
	}
	d.Summary = fmt.Sprintf("%d of %d core and %d of %d nice-to-have skills evidenced.", reqMet, reqTotal, prefMet, prefTotal)
	return d, matches
}

func scoreExperience(p Profile, j Job, weight float64) Dimension {
	d := Dimension{Key: "experience", Label: "Experience & seniority", Weight: weight}
	a := j.Analysis
	years := Component{Key: "years", Label: "Years of experience"}
	switch {
	case a.MinYears == nil:
		years.Detail = "The posting does not state a minimum number of years."
	case p.YearsExperience == nil:
		years.Detail = "Add your years of experience (or dated roles) to your profile."
	default:
		min, have := float64(*a.MinYears), *p.YearsExperience
		s := 1.0
		if have < min {
			s = math.Max(0, 1-yearsShortfallPenalty*(min-have)/min)
		}
		years.Score = ptr(round1(s * 100))
		src := "stated on your profile"
		if p.YearsSource == "COMPUTED" {
			src = "computed from your dated roles"
		}
		years.Detail = fmt.Sprintf("Requires %d+ years; you have %s (%s).", *a.MinYears, trimFloat(have), src)
	}
	level := Component{Key: "seniority", Label: "Seniority"}
	jr, jok := seniorityRank[a.Seniority]
	switch {
	case !jok:
		level.Detail = "The posting does not state a seniority level."
	case len(p.SeniorityLevels) == 0:
		level.Detail = "Set your target seniority levels in your preferences."
	default:
		best := 0.0
		for _, l := range p.SeniorityLevels {
			r, ok := seniorityRank[l]
			if !ok {
				continue
			}
			var s float64
			switch diff := abs(r - jr); {
			case l == a.Seniority:
				s = 1
			case diff == 0:
				s = 0.75 // same level, different track (e.g. Staff vs Lead)
			case diff == 1:
				s = 0.5
			}
			best = math.Max(best, s)
		}
		level.Score = ptr(round1(best * 100))
		level.Detail = fmt.Sprintf("The role is %s; you target %s.", strings.ToLower(a.Seniority), strings.ToLower(strings.Join(p.SeniorityLevels, ", ")))
	}
	d.Components = []Component{years, level}
	d.Score = combine([]Component{years, level}, []float64{0.6, 0.4})
	d.Summary = summarize(d.Components)
	return d
}

func scoreDomain(p Profile, j Job, weight float64) Dimension {
	d := Dimension{Key: "domain", Label: "Role & domain relevance", Weight: weight}
	sem := Component{Key: "semantic", Label: "Profile similarity"}
	if j.Similarity != nil {
		s := clamp((*j.Similarity-simLow)/(simHigh-simLow), 0, 1)
		sem.Score = ptr(round1(s * 100))
		sem.Detail = fmt.Sprintf("Semantic similarity between your profile and the posting: %.2f.", *j.Similarity)
	} else {
		sem.Detail = "Profile similarity is unavailable (add a headline, summary or experience)."
	}
	title := Component{Key: "title", Label: "Target role"}
	if len(p.TargetRoles) == 0 {
		title.Detail = "Add target roles to your preferences."
	} else {
		best, bestRole := 0.0, ""
		jt := titleTokens(j.Title)
		for _, role := range p.TargetRoles {
			rt := titleTokens(role)
			if len(rt) == 0 {
				continue
			}
			hit := 0
			for _, t := range rt {
				if slices.Contains(jt, t) {
					hit++
				}
			}
			if s := float64(hit) / float64(len(rt)); s > best {
				best, bestRole = s, role
			}
		}
		title.Score = ptr(round1(best * 100))
		if best > 0 {
			title.Detail = fmt.Sprintf("The title overlaps your target role “%s”.", bestRole)
		} else {
			title.Detail = "The title does not match any of your target roles."
		}
	}
	d.Components = []Component{sem, title}
	d.Score = combine(d.Components, []float64{0.6, 0.4})
	d.Summary = summarize(d.Components)
	return d
}

func scoreLocation(p Profile, j Job, weight float64, checks []Check) Dimension {
	d := Dimension{Key: "location", Label: "Location & eligibility", Weight: weight}
	elig := Component{Key: "eligibility", Label: "Location eligibility"}
	for _, c := range checks {
		if c.Key == "LOCATION_ELIGIBILITY" {
			elig.Detail = c.Detail
			switch c.Status {
			case CheckPass:
				elig.Score = ptr(100.0)
			case CheckFail:
				elig.Score = ptr(0.0)
			}
		}
	}
	arr := Component{Key: "arrangement", Label: "Work arrangement"}
	fit := map[string]map[string]float64{
		"REMOTE": {"REMOTE_ONLY": 100, "HYBRID_OK": 100, "ONSITE_OK": 100},
		"HYBRID": {"REMOTE_ONLY": 0, "HYBRID_OK": 80, "ONSITE_OK": 80},
		"ONSITE": {"REMOTE_ONLY": 0, "HYBRID_OK": 30, "ONSITE_OK": 70},
	}
	if s, ok := fit[j.RemoteStatus][p.RemoteMode]; ok {
		arr.Score = ptr(s)
		arr.Detail = fmt.Sprintf("The role is %s; your preference is %s.", strings.ToLower(j.RemoteStatus), remoteModeLabel(p.RemoteMode))
	} else if p.RemoteMode == "" {
		arr.Detail = "Set your remote-work preference."
	} else {
		arr.Detail = "The posting does not state whether the role is remote, hybrid or on-site."
	}
	d.Components = []Component{elig, arr}
	d.Score = combine(d.Components, []float64{0.5, 0.5})
	d.Summary = summarize(d.Components)
	return d
}

func scoreCompensation(p Profile, j Job, weight float64, checks []Check) Dimension {
	d := Dimension{Key: "compensation", Label: "Compensation & preferences", Weight: weight}
	sal := Component{Key: "salary", Label: "Salary"}
	if p.MinSalary == nil {
		sal.Detail = "Set a minimum salary in your preferences."
	}
	for _, c := range checks {
		if c.Key != "SALARY_FLOOR" {
			continue
		}
		sal.Detail = c.Detail
		switch c.Status {
		case CheckFail:
			sal.Score = ptr(0.0)
		case CheckPass:
			floor := annualize(*p.MinSalary, p.SalaryPeriod)
			if j.SalaryAnnualMin != nil && *j.SalaryAnnualMin >= floor {
				sal.Score = ptr(100.0) // the whole disclosed range clears the minimum
			} else {
				sal.Score = ptr(80.0) // only the top of the range does
			}
		}
	}
	emp := Component{Key: "employment", Label: "Employment type"}
	switch {
	case len(p.EmploymentTypes) == 0:
		emp.Detail = "Set your preferred employment types."
	case j.EmploymentType == "" || j.EmploymentType == "UNKNOWN":
		emp.Detail = "The posting does not state the employment type."
	case slices.Contains(p.EmploymentTypes, j.EmploymentType):
		emp.Score = ptr(100.0)
		emp.Detail = fmt.Sprintf("%s matches your preferences.", humanize(j.EmploymentType))
	default:
		emp.Score = ptr(0.0)
		emp.Detail = fmt.Sprintf("%s is not among your preferred employment types.", humanize(j.EmploymentType))
	}
	d.Components = []Component{sal, emp}
	d.Score = combine(d.Components, []float64{0.7, 0.3})
	d.Summary = summarize(d.Components)
	return d
}

// ---- helpers ----

// combine averages known components with the given relative weights.
func combine(cs []Component, weights []float64) *float64 {
	var num, den float64
	for i, c := range cs {
		if c.Score != nil {
			num += *c.Score * weights[i]
			den += weights[i]
		}
	}
	if den == 0 {
		return nil
	}
	return ptr(round1(num / den))
}

func summarize(cs []Component) string {
	var parts []string
	for _, c := range cs {
		if c.Score != nil {
			parts = append(parts, c.Detail)
		}
	}
	if len(parts) == 0 {
		return "Not enough information to score this dimension."
	}
	return strings.Join(parts, " ")
}

func annualize(v float64, period string) float64 {
	switch period {
	case "MONTH":
		return v * 12
	case "HOUR":
		return v * 2080
	default:
		return v
	}
}

var titleStop = map[string]bool{
	"and": true, "of": true, "the": true, "a": true, "an": true, "for": true, "in": true, "at": true, "to": true,
	"senior": true, "sr": true, "junior": true, "jr": true, "staff": true, "principal": true, "lead": true,
	"i": true, "ii": true, "iii": true, "iv": true, "remote": true, "mid": true, "level": true,
}

var titleSynonyms = map[string]string{"developer": "engineer", "dev": "engineer", "programmer": "engineer", "swe": "engineer",
	"backend": "back-end", "frontend": "front-end", "fullstack": "full-stack"}

func titleTokens(s string) []string {
	s = strings.ToLower(s)
	s = strings.NewReplacer("back end", "backend", "front end", "frontend", "full stack", "fullstack").Replace(s)
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '+' || r == '#')
	}) {
		if syn, ok := titleSynonyms[f]; ok {
			f = syn
		}
		if f == "" || titleStop[f] || slices.Contains(out, f) {
			continue
		}
		out = append(out, f)
	}
	return out
}

func remoteModeLabel(m string) string {
	switch m {
	case "REMOTE_ONLY":
		return "remote only"
	case "HYBRID_OK":
		return "remote or hybrid"
	case "ONSITE_OK":
		return "any arrangement"
	}
	return "not set"
}

func humanize(s string) string {
	s = strings.ToLower(strings.ReplaceAll(s, "_", " "))
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func normName(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }

func overlaps(a, b []string) bool { return len(intersect(a, b)) > 0 }

func intersect(a, b []string) []string {
	var out []string
	for _, x := range a {
		if slices.Contains(b, x) && !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

func union(a, b []string) []string {
	out := slices.Clone(a)
	for _, x := range b {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

func subset(a, b []string) bool {
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}

func dedupe(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func maxPtr(a, b *float64) *float64 {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	case *a > *b:
		return a
	}
	return b
}

func money(v float64) string {
	s := fmt.Sprintf("%.0f", v)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func trimFloat(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func ptr[T any](v T) *T { return &v }

func round1(v float64) float64 { return math.Round(v*10) / 10 }

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
