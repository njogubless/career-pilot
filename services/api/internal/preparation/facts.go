package preparation

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/careerpilot/careerpilot/services/api/internal/aiclient"
	"github.com/careerpilot/careerpilot/services/api/internal/matching"
	"github.com/careerpilot/careerpilot/services/api/internal/profiles"
)

// sheet is the fact sheet for one user: the VERIFIED facts documents may use,
// each under a stable reference, plus what the platform needs to render
// structural text (titles, employers, dates) itself.
type sheet struct {
	Facts     []aiclient.Fact
	Candidate aiclient.Candidate
	Hash      []byte
	entries   map[string]entry
	skills    map[string]string
}

type entry struct {
	Title, Sub, Dates, Location, URL string
}

func verified(p profiles.Provenance) bool { return p.Verification == profiles.Verified }

func ym(d *profiles.Date) string {
	if d == nil {
		return ""
	}
	return d.Format("2006-01")
}

func monthYear(d *profiles.Date) string {
	if d == nil {
		return ""
	}
	return d.Format("Jan 2006")
}

func dateRange(start, end *profiles.Date, current bool) string {
	s, e := monthYear(start), monthYear(end)
	if current {
		e = "Present"
	}
	switch {
	case s != "" && e != "":
		return s + " – " + e
	case s != "":
		return s
	default:
		return e
	}
}

func join(sep string, parts ...string) string {
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

// buildSheet assembles the fact sheet. notes are the user's own words for this
// document and become a citable fact ("note:1").
func buildSheet(p profiles.Profile, name, email, notes string, now time.Time) sheet {
	s := sheet{entries: map[string]entry{}, skills: map[string]string{}, Facts: []aiclient.Fact{}}
	add := func(f aiclient.Fact) { s.Facts = append(s.Facts, f) }

	if t := strings.TrimSpace(p.Basics.Summary); t != "" {
		add(aiclient.Fact{Ref: "basics:summary", Kind: "basics", Text: t})
	}
	if t := strings.TrimSpace(p.Basics.Headline); t != "" {
		add(aiclient.Fact{Ref: "basics:headline", Kind: "basics", Text: t})
	}

	var verifiedExp []profiles.Experience
	for _, e := range p.Experiences {
		if !verified(e.Provenance) {
			continue
		}
		verifiedExp = append(verifiedExp, e)
		ref := "exp:" + e.ID.String()
		fields := map[string]string{"title": e.Title, "employer": e.Employer, "location": e.Location,
			"start": ym(e.StartDate), "end": ym(e.EndDate)}
		if e.IsCurrent {
			fields["current"] = "true"
		}
		add(aiclient.Fact{Ref: ref, Kind: "experience", Text: join(". ", e.Title+" at "+e.Employer, e.Description), Fields: fields})
		for i, a := range e.Achievements {
			if a = strings.TrimSpace(a); a != "" {
				add(aiclient.Fact{Ref: fmt.Sprintf("%s#a%d", ref, i+1), Kind: "achievement", Text: a, Parent: ref})
			}
		}
		s.entries[ref] = entry{Title: e.Title, Sub: e.Employer, Dates: dateRange(e.StartDate, e.EndDate, e.IsCurrent), Location: e.Location}
	}

	var levels []string
	for _, e := range p.Education {
		if !verified(e.Provenance) {
			continue
		}
		ref := "edu:" + e.ID.String()
		lv := matching.DegreeLevel(e.Degree)
		if lv != "" {
			levels = append(levels, lv)
		}
		degree := join(", ", e.Degree, e.Field)
		add(aiclient.Fact{Ref: ref, Kind: "education", Text: join(", ", degree, e.Institution),
			Fields: map[string]string{"degree": e.Degree, "field": e.Field, "institution": e.Institution, "level": lv,
				"start": ym(e.StartDate), "end": ym(e.EndDate)}})
		title, sub := degree, e.Institution
		if title == "" {
			title, sub = e.Institution, ""
		}
		s.entries[ref] = entry{Title: title, Sub: sub, Dates: dateRange(e.StartDate, e.EndDate, false)}
	}

	for _, sk := range p.Skills {
		if !verified(sk.Provenance) {
			continue
		}
		ref := "skill:" + sk.ID.String()
		fields := map[string]string{"category": sk.Category}
		if sk.Level != nil {
			fields["level"] = *sk.Level
		}
		if sk.Years != nil {
			fields["years"] = fmt.Sprintf("%g", *sk.Years)
		}
		add(aiclient.Fact{Ref: ref, Kind: "skill", Text: sk.Name, Fields: fields})
		s.skills[ref] = sk.Name
	}

	for _, c := range p.Certifications {
		if !verified(c.Provenance) {
			continue
		}
		ref := "cert:" + c.ID.String()
		add(aiclient.Fact{Ref: ref, Kind: "certification", Text: join(", ", c.Name, c.Issuer),
			Fields: map[string]string{"name": c.Name, "issuer": c.Issuer, "issued": ym(c.IssuedDate), "expires": ym(c.ExpiresDate)}})
		s.entries[ref] = entry{Title: c.Name, Sub: c.Issuer, Dates: monthYear(c.IssuedDate), URL: c.CredentialURL}
	}

	for _, pr := range p.Projects {
		if !verified(pr.Provenance) {
			continue
		}
		ref := "proj:" + pr.ID.String()
		add(aiclient.Fact{Ref: ref, Kind: "project", Text: join(". ", pr.Name, pr.Description), Fields: map[string]string{"name": pr.Name}})
		s.entries[ref] = entry{Title: pr.Name, URL: pr.URL}
	}

	if n := strings.TrimSpace(notes); n != "" {
		add(aiclient.Fact{Ref: "note:1", Kind: "note", Text: n})
	}

	location := p.Basics.LocationCity
	if p.Basics.LocationCountry != nil {
		location = join(", ", location, *p.Basics.LocationCountry)
	}
	links := []string{}
	for _, l := range p.Basics.Links {
		links = append(links, l.URL)
	}
	if levels == nil {
		levels = []string{}
	}
	s.Candidate = aiclient.Candidate{Name: name, Email: email, Location: location, Links: links,
		YearsExperience: matching.ComputedYears(verifiedExp, now), DegreeLevels: levels}
	if s.Candidate.YearsExperience == nil && p.Basics.YearsExperience != nil {
		// The user's own statement of total experience (entered by them).
		y := *p.Basics.YearsExperience
		s.Candidate.YearsExperience = &y
	}

	h := sha256.New()
	_ = json.NewEncoder(h).Encode(struct {
		F []aiclient.Fact
		C aiclient.Candidate
	}{s.Facts, s.Candidate})
	s.Hash = h.Sum(nil)
	return s
}

// render fills structural text from the profile: contact details, entry
// headings (title, employer, dates) and skill names. The model never writes
// these, and user edits cannot change them except by editing the profile.
func (s sheet) render(c *Content) {
	for si := range c.Sections {
		for bi := range c.Sections[si].Blocks {
			b := &c.Sections[si].Blocks[bi]
			if b.Meta == nil {
				b.Meta = map[string]string{}
			}
			switch b.Type {
			case "contact":
				b.Text = s.Candidate.Name
				b.Refs = []string{}
				setMeta(b, "email", s.Candidate.Email)
				setMeta(b, "location", s.Candidate.Location)
				setMeta(b, "links", strings.Join(s.Candidate.Links, " · "))
			case "entry":
				if len(b.Refs) == 0 {
					continue
				}
				e, ok := s.entries[b.Refs[0]]
				if !ok {
					continue // unresolved: the validator reports it
				}
				b.Refs = b.Refs[:1]
				b.Text = join(" — ", e.Title, e.Sub)
				setMeta(b, "dates", e.Dates)
				setMeta(b, "location", e.Location)
				setMeta(b, "url", e.URL)
			case "skills":
				var names []string
				for _, r := range b.Refs {
					if n, ok := s.skills[r]; ok {
						names = append(names, n)
					}
				}
				b.Text = strings.Join(names, " · ")
			}
		}
	}
}

func setMeta(b *Block, k, v string) {
	if v == "" {
		delete(b.Meta, k)
		return
	}
	b.Meta[k] = v
}
