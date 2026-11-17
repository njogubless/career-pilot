package matching

import (
	"fmt"
	"strings"
)

// buildSignals lists concrete positive and negative factors. Every signal is
// derived from a computed component or check, never from free text.
func buildSignals(r Result, j Job) []Signal {
	out := []Signal{}
	pos := func(s string) { out = append(out, Signal{Kind: "POSITIVE", Text: s}) }
	neg := func(s string) { out = append(out, Signal{Kind: "NEGATIVE", Text: s}) }

	var matched []string
	for _, m := range r.Skills {
		if m.Importance != "PREFERRED" && (m.Status == "MATCHED" || m.Status == "PARTIAL") {
			matched = append(matched, m.Name)
		}
	}
	if len(matched) > 0 {
		pos("Evidence on your profile for " + listUpTo(matched, 6) + ".")
	}
	if len(r.MissingSkills) > 0 {
		neg("No evidence on your profile for " + listUpTo(r.MissingSkills, 6) + ".")
	}
	for _, d := range r.Dimensions {
		for _, c := range d.Components {
			if c.Score == nil || c.Key == "required" || c.Key == "preferred" || c.Key == "eligibility" {
				continue
			}
			switch {
			case *c.Score >= 80:
				pos(c.Detail)
			case *c.Score < 40:
				neg(c.Detail)
			}
		}
	}
	for _, c := range r.Checks {
		switch c.Status {
		case CheckPass:
			if c.Key == "LOCATION_ELIGIBILITY" || c.Key == "SPONSORSHIP" || c.Key == "WORK_AUTHORIZATION" {
				pos(c.Detail)
			}
		case CheckFail:
			neg(c.Detail)
		}
	}
	return dedupeSignals(out)
}

// explain produces short, factual sentences from the computed result.
func explain(r Result) []string {
	var out []string
	out = append(out, fmt.Sprintf("Suitability %.0f/100 with %s confidence: %.0f%% of the scoring weight had enough information; the rest is unknown and was left out rather than guessed.",
		r.Score, strings.ToLower(r.Confidence), r.KnownWeight))

	var best, worst *Dimension
	for i := range r.Dimensions {
		d := &r.Dimensions[i]
		if d.Score == nil {
			continue
		}
		if best == nil || *d.Score > *best.Score {
			best = d
		}
		if worst == nil || *d.Score < *worst.Score {
			worst = d
		}
	}
	if best != nil && worst != nil && best != worst {
		out = append(out, fmt.Sprintf("Strongest area: %s (%.0f). Weakest: %s (%.0f).", best.Label, *best.Score, worst.Label, *worst.Score))
	}

	switch r.Eligibility {
	case Ineligible:
		out = append(out, "Not eligible: "+r.Blockers[0])
	case Unknown:
		for _, c := range r.Checks {
			if c.Critical && c.Status == CheckUnknown {
				out = append(out, "Eligibility unconfirmed: "+c.Detail)
				break
			}
		}
	default:
		out = append(out, "No eligibility blockers were found.")
	}

	var evidence []string
	for _, m := range r.Skills {
		if m.Importance == "PREFERRED" || m.Credit == 0 {
			continue
		}
		switch m.ProfileSource {
		case "SKILL_VERIFIED":
			evidence = append(evidence, m.Name+" (confirmed on your profile)")
		case "SKILL_UNVERIFIED":
			evidence = append(evidence, m.Name+" (on your profile, unconfirmed)")
		default:
			evidence = append(evidence, m.Name+" (mentioned in your experience)")
		}
		if len(evidence) == 4 {
			break
		}
	}
	if len(evidence) > 0 {
		out = append(out, "Supporting evidence: "+strings.Join(evidence, "; ")+".")
	}
	if len(r.MissingSkills) > 0 {
		out = append(out, "Gaps: "+listUpTo(r.MissingSkills, 5)+".")
	}
	return out
}

func listUpTo(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(items[:n], ", "), len(items)-n)
}

func dedupeSignals(in []Signal) []Signal {
	seen := map[string]bool{}
	out := make([]Signal, 0, len(in))
	for _, s := range in {
		if seen[s.Text] {
			continue
		}
		seen[s.Text] = true
		out = append(out, s)
	}
	return out
}
