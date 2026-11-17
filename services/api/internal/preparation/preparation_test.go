package preparation

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/profiles"
)

var now = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

func date(s string) *profiles.Date {
	t, _ := time.Parse(time.DateOnly, s)
	return &profiles.Date{Time: t}
}

var (
	ver   = profiles.Provenance{Source: profiles.SourceUser, Verification: profiles.Verified}
	unver = profiles.Provenance{Source: profiles.SourceCV, Verification: profiles.Unverified}
	expID = uuid.MustParse("00000000-0000-0000-0000-0000000000e1")
)

func profile() profiles.Profile {
	country := "DE"
	return profiles.Profile{
		Basics: profiles.Basics{Headline: "Backend engineer", Summary: "Builds payment systems.", LocationCity: "Berlin",
			LocationCountry: &country, Links: []profiles.Link{{Label: "GitHub", URL: "https://github.com/ada"}}},
		Experiences: []profiles.Experience{
			{ID: expID, Employer: "Acme", Title: "Senior Engineer", StartDate: date("2020-03-01"), IsCurrent: true,
				Achievements: []string{"Cut costs by 30%.", " "}, Provenance: ver},
			{ID: uuid.MustParse("00000000-0000-0000-0000-000000000001"), Employer: "Ghost Corp", Title: "CTO", StartDate: date("2010-01-01"), EndDate: date("2019-01-01"), Provenance: unver},
		},
		Education: []profiles.Education{{ID: uuid.MustParse("00000000-0000-0000-0000-000000000002"), Institution: "TU Berlin", Degree: "MSc", Field: "Computer Science", Provenance: ver}},
		Skills: []profiles.Skill{
			{ID: uuid.MustParse("00000000-0000-0000-0000-000000000003"), Name: "Go", Provenance: ver},
			{ID: uuid.MustParse("00000000-0000-0000-0000-000000000004"), Name: "Kubernetes", Provenance: unver},
		},
	}
}

func refsOf(s sheet) map[string]string {
	out := map[string]string{}
	for _, f := range s.Facts {
		out[f.Ref] = f.Kind
	}
	return out
}

func TestSheetUsesVerifiedFactsOnly(t *testing.T) {
	s := buildSheet(profile(), "Ada", "ada@example.com", "I use the product daily.", now)
	refs := refsOf(s)
	if refs["exp:"+expID.String()] != "experience" || refs["exp:"+expID.String()+"#a1"] != "achievement" {
		t.Fatalf("missing verified experience or achievement: %v", refs)
	}
	if _, ok := refs["exp:"+expID.String()+"#a2"]; ok {
		t.Fatal("blank achievement must be skipped")
	}
	for _, f := range s.Facts {
		if strings.Contains(f.Text, "Ghost Corp") || f.Text == "Kubernetes" {
			t.Fatalf("unverified fact leaked into the sheet: %+v", f)
		}
	}
	if refs["note:1"] != "note" || refs["basics:summary"] != "basics" {
		t.Fatalf("notes/basics missing: %v", refs)
	}
	// Years come from verified roles only (2020-03 .. 2026-11 = 6.5), not the unverified 9-year role.
	if y := s.Candidate.YearsExperience; y == nil || *y != 6.5 {
		t.Fatalf("years = %v, want 6.5", y)
	}
	if len(s.Candidate.DegreeLevels) != 1 || s.Candidate.DegreeLevels[0] != "MASTERS" {
		t.Fatalf("degree levels = %v", s.Candidate.DegreeLevels)
	}
	if s.Candidate.Location != "Berlin, DE" || s.Candidate.Links[0] != "https://github.com/ada" {
		t.Fatalf("candidate = %+v", s.Candidate)
	}
}

func TestSheetHashChangesWithFacts(t *testing.T) {
	a := buildSheet(profile(), "Ada", "a@x", "", now)
	b := buildSheet(profile(), "Ada", "a@x", "", now)
	if !bytes.Equal(a.Hash, b.Hash) {
		t.Fatal("hash must be deterministic")
	}
	p := profile()
	p.Skills[1].Verification = profiles.Verified
	if c := buildSheet(p, "Ada", "a@x", "", now); bytes.Equal(a.Hash, c.Hash) {
		t.Fatal("verifying a fact must change the hash")
	}
}

func cvContent() Content {
	return Content{SchemaVersion: SchemaVersion, Kind: KindCV, Sections: []Section{
		{Key: "contact", Title: "Contact", Blocks: []Block{{ID: "b1", Type: "contact", Text: "Mallory", Meta: map[string]string{"email": "evil@x"}}}},
		{Key: "experience", Title: "Experience", Blocks: []Block{
			{ID: "b2", Type: "entry", Text: "CEO — Google", Refs: []string{"exp:" + expID.String()}, Meta: map[string]string{"dates": "1990"}},
			{ID: "b3", Type: "bullet", Text: "  Cut costs by 30%.\r\n", Refs: []string{"exp:" + expID.String() + "#a1"}, Meta: map[string]string{"parent": "b2", "x": "y"}},
		}},
		{Key: "q1", Title: "Why us?", Blocks: []Block{{ID: "b4", Type: "answer", Text: "Because.", Meta: map[string]string{"status": "NEEDS_INPUT", "question_for_user": "Why?"}}}},
	}}
}

func TestSanitizeAndRenderKeepStructureAuthoritative(t *testing.T) {
	c := cvContent()
	if err := c.sanitize(KindCV); err != nil {
		t.Fatal(err)
	}
	buildSheet(profile(), "Ada Lovelace", "ada@example.com", "", now).render(&c)
	contact := c.Sections[0].Blocks[0]
	if contact.Text != "Ada Lovelace" || contact.Meta["email"] != "ada@example.com" {
		t.Fatalf("contact must come from the account: %+v", contact)
	}
	entry := c.Sections[1].Blocks[0]
	if entry.Text != "Senior Engineer — Acme" || entry.Meta["dates"] != "Mar 2020 – Present" {
		t.Fatalf("entry must be rendered from the profile, got %+v", entry)
	}
	bullet := c.Sections[1].Blocks[1]
	if bullet.Text != "Cut costs by 30%." || bullet.Meta["x"] != "" || bullet.Meta["parent"] != "b2" {
		t.Fatalf("bullet = %+v", bullet)
	}
	if ans := c.Sections[2].Blocks[0]; ans.Meta["status"] != "" {
		t.Fatalf("writing an answer must clear NEEDS_INPUT: %+v", ans)
	}
}

func TestSanitizeRejectsBadContent(t *testing.T) {
	cases := map[string]func(*Content){
		"kind":       func(c *Content) { c.Kind = KindCover },
		"schema":     func(c *Content) { c.SchemaVersion = "doc-v9" },
		"dup id":     func(c *Content) { c.Sections[1].Blocks[1].ID = "b2" },
		"block type": func(c *Content) { c.Sections[1].Blocks[1].Type = "script" },
		"key":        func(c *Content) { c.Sections[1].Key = "Bad Key" },
		"long":       func(c *Content) { c.Sections[1].Blocks[1].Text = strings.Repeat("x", maxBlockText+1) },
		"empty":      func(c *Content) { c.Sections = nil },
	}
	for name, mutate := range cases {
		c := cvContent()
		mutate(&c)
		err := c.sanitize(KindCV)
		var he *httpx.Error
		if err == nil || !errorsAs(err, &he) || he.Code != httpx.CodeValidationFailed {
			t.Errorf("%s: expected validation error, got %v", name, err)
		}
	}
}

func errorsAs(err error, target **httpx.Error) bool {
	he, ok := err.(*httpx.Error)
	if ok {
		*target = he
	}
	return ok
}

func rendered() Content {
	c := cvContent()
	_ = c.sanitize(KindCV)
	c.Sections[1].Blocks[1].Text = `Cut costs by 30% <script>&"`
	buildSheet(profile(), "Ada", "ada@example.com", "", now).render(&c)
	c.Sections[2].Blocks[0] = Block{ID: "b4", Type: "answer", Meta: map[string]string{"status": "NEEDS_INPUT", "question_for_user": "Why us?"}}
	return c
}

func TestDOCXIsValidAndEscaped(t *testing.T) {
	data, mime, name, err := render(rendered(), FormatDOCX, "Acme Pay GmbH", 3)
	if err != nil {
		t.Fatal(err)
	}
	if name != "tailored-cv-acme-pay-gmbh-v3.docx" || !strings.Contains(mime, "wordprocessingml") {
		t.Fatalf("name=%q mime=%q", name, mime)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		files[f.Name] = string(b)
		dec := xml.NewDecoder(bytes.NewReader(b))
		for {
			if _, err := dec.Token(); err == io.EOF {
				break
			} else if err != nil {
				t.Fatalf("%s is not well-formed XML: %v", f.Name, err)
			}
		}
	}
	doc := files["word/document.xml"]
	for _, want := range []string{"Senior Engineer — Acme", "Mar 2020 – Present", "&lt;script&gt;&amp;&#34;", "Answer needed: Why us?"} {
		if !strings.Contains(doc, want) {
			t.Errorf("document.xml missing %q", want)
		}
	}
	if _, ok := files["[Content_Types].xml"]; !ok {
		t.Fatal("missing content types part")
	}
	again, _, _, _ := render(rendered(), FormatDOCX, "Acme Pay GmbH", 3)
	if !bytes.Equal(data, again) {
		t.Fatal("DOCX export must be reproducible")
	}
}

func TestTextAndMarkdownExport(t *testing.T) {
	txt, _, _, _ := render(rendered(), FormatTXT, "", 1)
	s := string(txt)
	for _, want := range []string{"Ada\n", "EXPERIENCE\n", "Senior Engineer — Acme  (Mar 2020 – Present)", "• Cut costs", "[Answer needed: Why us?]"} {
		if !strings.Contains(s, want) {
			t.Errorf("txt missing %q in:\n%s", want, s)
		}
	}
	md, _, name, _ := render(rendered(), FormatMD, "", 1)
	if name != "tailored-cv-v1.md" {
		t.Errorf("name = %q", name)
	}
	if !strings.Contains(string(md), `\<script\>`) || !strings.Contains(string(md), "## Experience") {
		t.Errorf("markdown not escaped or missing headings:\n%s", md)
	}
	if _, _, _, err := render(rendered(), "pdf", "", 1); err == nil {
		t.Fatal("pdf is produced by the browser, not the server")
	}
}

func TestCoverLetterClosingGetsName(t *testing.T) {
	c := Content{SchemaVersion: SchemaVersion, Kind: KindCover, Sections: []Section{
		{Key: "contact", Title: "Contact", Blocks: []Block{{ID: "b1", Type: "contact"}}},
		{Key: "letter", Title: "Letter", Blocks: []Block{
			{ID: "b2", Type: "paragraph", Text: "Dear Hiring Team,", Meta: map[string]string{"role": "greeting"}},
			{ID: "b3", Type: "paragraph", Text: "Kind regards,", Meta: map[string]string{"role": "closing"}},
		}},
	}}
	buildSheet(profile(), "Ada", "", "", now).render(&c)
	txt := string(renderText(c))
	if strings.Contains(txt, "LETTER") || !strings.Contains(txt, "Kind regards,\nAda") {
		t.Fatalf("letter layout wrong:\n%s", txt)
	}
}

func TestFromProfileCopiesVerifiedFactsVerbatim(t *testing.T) {
	sh := buildSheet(profile(), "Ada", "a@x", "", now)
	c := fromProfile(KindCV, sh, nil)
	if err := c.sanitize(KindCV); err != nil {
		t.Fatal(err)
	}
	sh.render(&c)
	var keys []string
	var texts []string
	for _, s := range c.Sections {
		keys = append(keys, s.Key)
		for _, b := range s.Blocks {
			texts = append(texts, b.Text)
		}
	}
	if strings.Join(keys, ",") != "contact,summary,experience,skills,education" {
		t.Fatalf("sections = %v", keys)
	}
	all := strings.Join(texts, "\n")
	for _, want := range []string{"Builds payment systems.", "Senior Engineer — Acme", "Cut costs by 30%.", "Go", "MSc, Computer Science — TU Berlin"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "Ghost Corp") || strings.Contains(all, "Kubernetes") {
		t.Fatal("unverified facts in template")
	}

	letter := fromProfile(KindCover, sh, nil)
	if b := letter.Sections[1].Blocks; len(b) != 3 || b[1].Text != "" || b[0].Meta["role"] != "greeting" {
		t.Fatalf("letter template = %+v", b)
	}
	answers := fromProfile(KindAnswers, sh, []string{"Why us?", "Salary?"})
	if len(answers.Sections) != 2 || answers.Sections[1].Title != "Salary?" || answers.Sections[0].Blocks[0].Meta["status"] != "NEEDS_INPUT" {
		t.Fatalf("answers template = %+v", answers.Sections)
	}
}
