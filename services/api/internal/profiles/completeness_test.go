package profiles

import "testing"

func TestCompletenessWeightsSumTo100(t *testing.T) {
	total := 0
	for _, c := range checks {
		total += c.item.Weight
	}
	if total != 100 {
		t.Fatalf("weights sum to %d", total)
	}
}

func TestCompletenessEmptyProfile(t *testing.T) {
	c := computeCompleteness(Profile{})
	// An empty profile has no unverified facts, so only "verification" passes.
	if c.Score != 10 || len(c.Missing) != len(checks)-1 {
		t.Fatalf("score=%d missing=%d", c.Score, len(c.Missing))
	}
}

func TestCompletenessIgnoresRejectedAndCountsUnverified(t *testing.T) {
	country := "KE"
	p := Profile{
		Basics:      Basics{Headline: "Engineer", LocationCountry: &country},
		Experiences: []Experience{{Provenance: Provenance{SourceCV, Rejected}}},
		Skills: []Skill{
			{Provenance: Provenance{SourceCV, Unverified}}, {Provenance: Provenance{SourceUser, Verified}},
		},
	}
	c := computeCompleteness(p)
	if c.UnverifiedFacts != 1 {
		t.Fatalf("unverified = %d", c.UnverifiedFacts)
	}
	for _, m := range c.Missing {
		if m.Key == "headline" || m.Key == "location_country" {
			t.Fatalf("%s should be satisfied", m.Key)
		}
	}
	has := func(k string) bool {
		for _, m := range c.Missing {
			if m.Key == k {
				return true
			}
		}
		return false
	}
	if !has("experience") || !has("verification") || !has("skills") {
		t.Fatalf("expected experience/verification/skills missing: %+v", c.Missing)
	}
}

func TestNormalizeSkill(t *testing.T) {
	if NormalizeSkill("  Go   Lang ") != "go lang" {
		t.Fatal("normalisation failed")
	}
}
