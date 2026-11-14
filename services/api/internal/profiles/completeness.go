package profiles

// Completeness tells the user what is missing and why it matters for
// matching (MASTER_BUILD_PROMPT §5: "profile completeness indicators").
type Completeness struct {
	Score   int           `json:"score"` // 0..100
	Missing []MissingItem `json:"missing"`
	// UnverifiedFacts counts imported/inferred facts awaiting user review.
	UnverifiedFacts int `json:"unverified_facts"`
}

type MissingItem struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Impact string `json:"impact"`
	Weight int    `json:"weight"`
}

type check struct {
	item MissingItem
	ok   func(Profile) bool
}

var checks = []check{
	{MissingItem{"headline", "Add a professional headline", "Used to summarise you to recruiters and in tailored documents.", 10},
		func(p Profile) bool { return p.Basics.Headline != "" }},
	{MissingItem{"location_country", "Set your country of residence", "Needed to evaluate location-restricted roles.", 10},
		func(p Profile) bool { return p.Basics.LocationCountry != nil }},
	{MissingItem{"summary", "Write a short professional summary", "Improves semantic matching and cover letters.", 5},
		func(p Profile) bool { return len(p.Basics.Summary) >= 40 }},
	{MissingItem{"experience", "Add at least one work experience", "Experience drives seniority and domain matching.", 20},
		func(p Profile) bool {
			return countActive(p.Experiences, func(e Experience) Verification { return e.Verification }) > 0
		}},
	{MissingItem{"skills", "Add at least five skills", "Skills carry 35% of the match score.", 15},
		func(p Profile) bool {
			return countActive(p.Skills, func(s Skill) Verification { return s.Verification }) >= 5
		}},
	{MissingItem{"target_roles", "Choose target roles", "Determines which jobs discovery searches for.", 10},
		func(p Profile) bool { return len(p.Preferences.TargetRoles) > 0 }},
	{MissingItem{"work_eligible_countries", "List countries where you can legally work", "Required for honest eligibility checks; without it, eligibility is reported as unknown.", 15},
		func(p Profile) bool { return len(p.Preferences.WorkEligibleCountries) > 0 }},
	{MissingItem{"salary", "Set a minimum salary (optional)", "Lets matching flag roles that disclose pay below your floor.", 5},
		func(p Profile) bool { return p.Preferences.MinSalary != nil }},
	{MissingItem{"verification", "Review imported facts", "Unverified facts are not used in generated applications.", 10},
		func(p Profile) bool { return unverifiedCount(p) == 0 }},
}

func countActive[T any](items []T, ver func(T) Verification) int {
	n := 0
	for _, it := range items {
		if ver(it) != Rejected {
			n++
		}
	}
	return n
}

func unverifiedCount(p Profile) int {
	n := 0
	for _, e := range p.Experiences {
		if e.Verification == Unverified {
			n++
		}
	}
	for _, e := range p.Education {
		if e.Verification == Unverified {
			n++
		}
	}
	for _, s := range p.Skills {
		if s.Verification == Unverified {
			n++
		}
	}
	for _, c := range p.Certifications {
		if c.Verification == Unverified {
			n++
		}
	}
	for _, pr := range p.Projects {
		if pr.Verification == Unverified {
			n++
		}
	}
	return n
}

// computeCompleteness is pure so it can be unit-tested exhaustively.
func computeCompleteness(p Profile) Completeness {
	c := Completeness{Missing: []MissingItem{}, UnverifiedFacts: unverifiedCount(p)}
	for _, ch := range checks {
		if ch.ok(p) {
			c.Score += ch.item.Weight
		} else {
			c.Missing = append(c.Missing, ch.item)
		}
	}
	return c
}
