package sources

import (
	"slices"
	"strings"
	"testing"
)

func TestClassifyLocation(t *testing.T) {
	cases := []struct {
		loc, workplace, status, scope string
		countries                     []string
		region                        string
	}{
		{"Remote, United States", "", "REMOTE", ScopeCountries, []string{"US"}, ""},
		{"Remote - US", "", "REMOTE", ScopeCountries, []string{"US"}, ""},
		{"Remote (Canada)", "", "REMOTE", ScopeCountries, []string{"CA"}, ""},
		{"Remote, Worldwide", "", "REMOTE", ScopeWorldwide, nil, ""},
		{"Anywhere", "remote", "REMOTE", ScopeWorldwide, nil, ""},
		{"Remote EMEA", "", "REMOTE", ScopeRegion, nil, "EMEA"},
		{"North America", "remote", "REMOTE", ScopeRegion, nil, "North America"},
		{"Remote", "", "REMOTE", ScopeUnknown, nil, ""},
		{"Dublin, Ireland", "", "UNKNOWN", ScopeNotRemote, nil, ""}, // no workplace info: not assumed onsite
		{"London, United Kingdom", "onsite", "ONSITE", ScopeNotRemote, nil, ""},
		{"New York (Hybrid)", "", "HYBRID", ScopeNotRemote, nil, ""},
		{"Remote - join us anywhere in Kenya", "", "REMOTE", ScopeCountries, []string{"KE"}, ""}, // a named country beats "anywhere"
	}
	for _, c := range cases {
		status, scope, countries, region := ClassifyLocation(c.loc, c.workplace)
		if status != c.status || scope != c.scope || !slices.Equal(countries, c.countries) || region != c.region {
			t.Errorf("ClassifyLocation(%q,%q) = %s %s %v %q; want %s %s %v %q", c.loc, c.workplace,
				status, scope, countries, region, c.status, c.scope, c.countries, c.region)
		}
	}
}

func TestCountriesInIgnoresLowercaseWords(t *testing.T) {
	if got := CountriesIn("Work with us in Nairobi"); len(got) != 0 {
		t.Fatalf("lowercase 'us' must not map to US: %v", got)
	}
	if got := CountriesIn("Berlin, Germany; Remote - UK"); !slices.Equal(got, []string{"DE", "GB"}) {
		t.Fatalf("got %v", got)
	}
}

// Regressions found by measuring real ingested data (2026-11-14).
func TestCountriesInRealWorldRegressions(t *testing.T) {
	cases := map[string][]string{
		"SIHO - Columbus, IN": nil, // Indiana, not India
		"Remote - CA":         nil, // California or Canada: ambiguous
		"Wilmington, DE":      nil, // Delaware, not Germany
		"San Francisco, Seattle, Remote in the US": {"US"}, // US inside a phrase
		"Remote - USA": {"US"},
		"Austria, Remote ; Germany, Remote; Poland, Remote": {"AT", "DE", "PL"},
		"Toronto, Canada": {"CA"}, // full name is unambiguous
	}
	for in, want := range cases {
		if got := CountriesIn(in); !slices.Equal(got, want) {
			t.Errorf("CountriesIn(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSanitizeHTMLRemovesActiveContent(t *testing.T) {
	in := `<p onclick="x()">Hi <a href="javascript:alert(1)">bad</a><a href="https://ok.example">ok</a></p><script>alert(1)</script><img src=x onerror=alert(1)><style>body{}</style>`
	out := SanitizeHTML(in)
	for _, bad := range []string{"<script", "onclick", "javascript:", "onerror", "<style"} {
		if strings.Contains(out, bad) {
			t.Errorf("sanitised output still contains %q: %s", bad, out)
		}
	}
	if !strings.Contains(out, `href="https://ok.example"`) || !strings.Contains(out, `rel="nofollow`) {
		t.Errorf("safe link not preserved with nofollow: %s", out)
	}
}

func TestHTMLToText(t *testing.T) {
	got := HTMLToText("<h2>About</h2><p>We&apos;re hiring&nbsp;engineers.</p><ul><li>Go</li><li>SQL</li></ul>")
	want := "About\n\nWe're hiring engineers.\n\nGo\n\nSQL"
	if strings.ReplaceAll(got, "\n\n", "\n") != strings.ReplaceAll(want, "\n\n", "\n") {
		t.Fatalf("got %q", got)
	}
}

func TestAnnualizeNeverConvertsCurrency(t *testing.T) {
	v := 5000.0
	if a := Annualize(&v, "MONTH"); a == nil || *a != 60000 {
		t.Fatalf("monthly: %v", a)
	}
	if Annualize(&v, "WEEK") != nil || Annualize(nil, "YEAR") != nil {
		t.Fatal("unknown period or nil must stay unknown")
	}
}

func TestDedupeKeyNormalises(t *testing.T) {
	a := DedupeKey(Posting{Company: "Acme, Inc.", Title: "Senior  Engineer", LocationRaw: "Remote - US"})
	b := DedupeKey(Posting{Company: "acme inc", Title: "senior engineer", LocationRaw: "remote us"})
	if a != b {
		t.Fatalf("%q != %q", a, b)
	}
}

func TestEmploymentType(t *testing.T) {
	for in, want := range map[string]string{"Full-time": "FULL_TIME", "FullTime": "FULL_TIME", "Contract": "CONTRACT", "Intern": "INTERNSHIP", "": "UNKNOWN"} {
		if got := EmploymentType(in); got != want {
			t.Errorf("EmploymentType(%q) = %s", in, got)
		}
	}
}
