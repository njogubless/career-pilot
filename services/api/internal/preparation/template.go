package preparation

import (
	"fmt"
	"slices"
	"strings"

	"github.com/careerpilot/careerpilot/services/api/internal/aiclient"
)

// OriginTemplate marks a version built deterministically from the profile
// (no AI): facts copied verbatim, ready for the user to tailor by hand.
const OriginTemplate = "TEMPLATE"

type blockSeq struct{ n int }

func (i *blockSeq) next() string {
	i.n++
	return fmt.Sprintf("b%d", i.n)
}

// fromProfile builds a starting document from verified facts only. It never
// invents text: CV bullets are the user's own achievements and descriptions;
// letters and answers start empty for the user to write.
func fromProfile(kind string, sh sheet, questions []string) Content {
	var id blockSeq
	contact := Section{Key: "contact", Title: "Contact", Blocks: []Block{{ID: id.next(), Type: "contact", Refs: []string{}, Meta: map[string]string{}}}}
	block := func(typ, text string, refs []string, meta map[string]string) Block {
		if meta == nil {
			meta = map[string]string{}
		}
		if refs == nil {
			refs = []string{}
		}
		return Block{ID: id.next(), Type: typ, Text: text, Refs: refs, Meta: meta}
	}
	byKind := map[string][]aiclient.Fact{}
	children := map[string][]aiclient.Fact{}
	for _, f := range sh.Facts {
		byKind[f.Kind] = append(byKind[f.Kind], f)
		if f.Parent != "" {
			children[f.Parent] = append(children[f.Parent], f)
		}
	}

	switch kind {
	case KindCover:
		letter := Section{Key: "letter", Title: "Letter", Blocks: []Block{
			block("paragraph", "Dear Hiring Team,", nil, map[string]string{"role": "greeting"}),
			block("paragraph", "", nil, nil),
			block("paragraph", "Kind regards,", nil, map[string]string{"role": "closing"}),
		}}
		return Content{SchemaVersion: SchemaVersion, Kind: kind, Sections: []Section{contact, letter}}
	case KindAnswers:
		var sections []Section
		for i, q := range questions {
			sections = append(sections, Section{Key: fmt.Sprintf("q%d", i+1), Title: q, Blocks: []Block{
				block("answer", "", nil, map[string]string{"status": "NEEDS_INPUT", "question_for_user": "Write your answer."}),
			}})
		}
		return Content{SchemaVersion: SchemaVersion, Kind: kind, Sections: sections}
	}

	sections := []Section{contact}
	for _, f := range byKind["basics"] {
		if f.Ref == "basics:summary" {
			sections = append(sections, Section{Key: "summary", Title: "Summary", Blocks: []Block{block("paragraph", f.Text, []string{f.Ref}, nil)}})
		}
	}
	exps := slices.Clone(byKind["experience"])
	slices.SortStableFunc(exps, func(a, b aiclient.Fact) int {
		ka := a.Fields["current"] + a.Fields["start"]
		kb := b.Fields["current"] + b.Fields["start"]
		return strings.Compare(kb, ka) // current roles first, then newest start
	})
	var expBlocks []Block
	for _, e := range exps {
		entry := block("entry", "", []string{e.Ref}, nil)
		expBlocks = append(expBlocks, entry)
		achievements := children[e.Ref]
		for _, a := range achievements {
			expBlocks = append(expBlocks, block("bullet", a.Text, []string{a.Ref}, map[string]string{"parent": entry.ID}))
		}
		// Without achievements, the role's own description becomes its bullet.
		desc := strings.TrimPrefix(strings.TrimPrefix(e.Text, e.Fields["title"]+" at "+e.Fields["employer"]), ". ")
		if len(achievements) == 0 && strings.TrimSpace(desc) != "" {
			expBlocks = append(expBlocks, block("bullet", desc, []string{e.Ref}, map[string]string{"parent": entry.ID}))
		}
	}
	if len(expBlocks) > 0 {
		sections = append(sections, Section{Key: "experience", Title: "Experience", Blocks: expBlocks})
	}
	var projBlocks []Block
	for _, p := range byKind["project"] {
		entry := block("entry", "", []string{p.Ref}, nil)
		projBlocks = append(projBlocks, entry)
		if desc := strings.TrimPrefix(strings.TrimPrefix(p.Text, p.Fields["name"]), ". "); strings.TrimSpace(desc) != "" {
			projBlocks = append(projBlocks, block("bullet", desc, []string{p.Ref}, map[string]string{"parent": entry.ID}))
		}
	}
	if len(projBlocks) > 0 {
		sections = append(sections, Section{Key: "projects", Title: "Projects", Blocks: projBlocks})
	}
	var skillRefs []string
	for _, s := range byKind["skill"] {
		skillRefs = append(skillRefs, s.Ref)
	}
	if len(skillRefs) > 0 {
		sections = append(sections, Section{Key: "skills", Title: "Skills", Blocks: []Block{block("skills", "", skillRefs, nil)}})
	}
	for _, g := range []struct{ key, title, kind string }{{"education", "Education", "education"}, {"certifications", "Certifications", "certification"}} {
		var bs []Block
		for _, f := range byKind[g.kind] {
			bs = append(bs, block("entry", "", []string{f.Ref}, nil))
		}
		if len(bs) > 0 {
			sections = append(sections, Section{Key: g.key, Title: g.title, Blocks: bs})
		}
	}
	return Content{SchemaVersion: SchemaVersion, Kind: kind, Sections: sections}
}
