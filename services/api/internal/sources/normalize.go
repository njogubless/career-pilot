package sources

import (
	"crypto/sha256"
	"fmt"
	"html"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/microcosm-cc/bluemonday"
)

// ---- HTML ----

var (
	descriptionPolicy = func() *bluemonday.Policy {
		p := bluemonday.UGCPolicy()
		p.AllowElements("h1", "h2", "h3", "h4", "p", "ul", "ol", "li", "strong", "em", "b", "i", "br", "blockquote")
		p.RequireNoFollowOnLinks(true)
		p.AddTargetBlankToFullyQualifiedLinks(true)
		p.AllowURLSchemes("http", "https", "mailto")
		return p
	}()
	stripPolicy = bluemonday.StrictPolicy()
	spaceRe     = regexp.MustCompile(`[ \t\x{00A0}]+`)
	blankRe     = regexp.MustCompile(`\n{3,}`)
	blockTagRe  = regexp.MustCompile(`(?i)</?(p|div|br|li|ul|ol|h[1-6]|tr)[^>]*>`)
)

// SanitizeHTML removes scripts, event handlers, styles and unsafe URLs from
// third-party job descriptions before storage and display.
func SanitizeHTML(raw string) string { return descriptionPolicy.Sanitize(raw) }

// HTMLToText produces readable plain text for search and AI extraction.
func HTMLToText(raw string) string {
	s := blockTagRe.ReplaceAllString(raw, "\n")
	s = html.UnescapeString(stripPolicy.Sanitize(s))
	s = spaceRe.ReplaceAllString(s, " ")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	return strings.TrimSpace(blankRe.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}

// ---- Locations ----

var countryNames = map[string]string{
	"united states": "US", "united states of america": "US", "usa": "US", "us": "US", "u.s.": "US",
	"canada": "CA", "mexico": "MX", "brazil": "BR", "argentina": "AR", "colombia": "CO", "chile": "CL",
	"united kingdom": "GB", "uk": "GB", "england": "GB", "scotland": "GB", "ireland": "IE", "germany": "DE",
	"france": "FR", "spain": "ES", "portugal": "PT", "italy": "IT", "netherlands": "NL", "belgium": "BE",
	"switzerland": "CH", "austria": "AT", "sweden": "SE", "norway": "NO", "denmark": "DK", "finland": "FI",
	"poland": "PL", "czech republic": "CZ", "czechia": "CZ", "romania": "RO", "greece": "GR", "estonia": "EE",
	"lithuania": "LT", "latvia": "LV", "ukraine": "UA", "serbia": "RS", "hungary": "HU", "bulgaria": "BG",
	"kenya": "KE", "nigeria": "NG", "south africa": "ZA", "egypt": "EG", "ghana": "GH", "uganda": "UG",
	"rwanda": "RW", "tanzania": "TZ", "ethiopia": "ET", "morocco": "MA", "israel": "IL", "turkey": "TR",
	"united arab emirates": "AE", "uae": "AE", "saudi arabia": "SA", "india": "IN", "pakistan": "PK",
	"bangladesh": "BD", "singapore": "SG", "japan": "JP", "south korea": "KR", "korea": "KR", "china": "CN",
	"hong kong": "HK", "taiwan": "TW", "philippines": "PH", "indonesia": "ID", "vietnam": "VN",
	"thailand": "TH", "malaysia": "MY", "australia": "AU", "new zealand": "NZ",
}

// Two-letter ISO codes only accepted when written in capitals as a separate
// token (e.g. "Remote - US"), to avoid matching ordinary words.
var isoCodes = func() map[string]bool {
	m := map[string]bool{}
	for _, c := range countryNames {
		m[c] = true
	}
	return m
}()

// ambiguousCodes are ISO country codes that are also US state abbreviations
// ("Columbus, IN" is Indiana, not India). As bare tokens they are ignored, so
// eligibility stays unknown rather than wrong.
var ambiguousCodes = map[string]bool{
	"AL": true, "AR": true, "CA": true, "CO": true, "DE": true, "GA": true, "ID": true, "IL": true, "IN": true,
	"LA": true, "MA": true, "MD": true, "ME": true, "MN": true, "MS": true, "MT": true, "NC": true, "NE": true,
	"PA": true, "SC": true, "SD": true, "TN": true, "VA": true,
}

var usUKWordRe = regexp.MustCompile(`\b(USA|US|UK)\b`)

// NormalizerVersion is mixed into the content hash so improvements to
// normalisation reprocess existing postings on their next sync.
const NormalizerVersion = "3"

var regionNames = map[string]string{
	"emea": "EMEA", "europe": "Europe", "eu": "Europe", "americas": "Americas", "north america": "North America",
	"latam": "Latin America", "latin america": "Latin America", "south america": "Latin America",
	"apac": "APAC", "asia": "Asia", "asia pacific": "APAC", "africa": "Africa", "middle east": "Middle East",
}

var (
	worldwideRe = regexp.MustCompile(`(?i)\b(worldwide|anywhere|global(ly)?|international)\b`)
	remoteRe    = regexp.MustCompile(`(?i)\bremote\b`)
	hybridRe    = regexp.MustCompile(`(?i)\bhybrid\b`)
)

// CountriesIn extracts ISO country codes explicitly named in a location string.
func CountriesIn(loc string) []string {
	var out []string
	add := func(c string) {
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	parts := strings.FieldsFunc(loc, func(r rune) bool { return strings.ContainsRune(",;/|()-–•", r) })
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// Bare two-letter codes count only in capitals ("Remote - US"), so the
		// English word "us" is never read as the United States.
		if len(p) == 2 {
			if strings.ToUpper(p) == p && !ambiguousCodes[p] {
				if isoCodes[p] {
					add(p)
				} else if c, ok := countryNames[strings.ToLower(p)]; ok { // aliases such as "UK"
					add(c)
				}
			}
			continue
		}
		key := strings.ToLower(strings.TrimSuffix(p, "."))
		if c, ok := countryNames[key]; ok {
			add(c)
			continue
		}
		// Upper-case US/USA/UK as whole words ("Remote in the US").
		if m := usUKWordRe.FindString(p); m != "" {
			if m == "UK" {
				add("GB")
			} else {
				add("US")
			}
		}
		// Longer fragments such as "Remote United States".
		for name, c := range countryNames {
			if len(name) > 3 && strings.Contains(" "+key+" ", " "+name+" ") {
				add(c)
			}
		}
	}
	slices.Sort(out)
	return out
}

func regionIn(loc string) string {
	l := " " + strings.ToLower(loc) + " "
	for _, name := range []string{"north america", "latin america", "south america", "asia pacific", "middle east", "emea", "europe", "americas", "latam", "apac", "africa", "asia", "eu"} {
		if strings.Contains(l, " "+name+" ") || strings.Contains(l, "("+name+")") || strings.Contains(l, name+",") {
			return regionNames[name]
		}
	}
	return ""
}

// ClassifyLocation derives remote status and scope from a location string and
// an optional structured workplace type. It is deliberately conservative:
// anything not explicitly stated is UNKNOWN.
func ClassifyLocation(loc, workplace string) (status, scope string, remoteCountries []string, region string) {
	switch strings.ToLower(workplace) {
	case "remote":
		status = "REMOTE"
	case "hybrid":
		status = "HYBRID"
	case "onsite", "on-site", "in_office", "inoffice":
		status = "ONSITE"
	default:
		switch {
		case hybridRe.MatchString(loc):
			status = "HYBRID"
		case remoteRe.MatchString(loc):
			status = "REMOTE"
		default:
			status = "UNKNOWN"
		}
	}
	if status != "REMOTE" {
		return status, ScopeNotRemote, nil, ""
	}
	// A named country beats a worldwide keyword ("anywhere in Kenya"): never
	// over-claim where applicants may be located.
	if cs := CountriesIn(loc); len(cs) > 0 {
		return status, ScopeCountries, cs, ""
	}
	if worldwideRe.MatchString(loc) {
		return status, ScopeWorldwide, nil, ""
	}
	if r := regionIn(loc); r != "" {
		return status, ScopeRegion, nil, r
	}
	return status, ScopeUnknown, nil, ""
}

// ---- Employment type ----

func EmploymentType(raw string) string {
	s := strings.ToLower(strings.NewReplacer("-", "", "_", "", " ", "").Replace(raw))
	switch {
	case strings.Contains(s, "fulltime"), s == "permanent":
		return "FULL_TIME"
	case strings.Contains(s, "parttime"):
		return "PART_TIME"
	case strings.Contains(s, "contract"), strings.Contains(s, "freelance"):
		return "CONTRACT"
	case strings.Contains(s, "intern"):
		return "INTERNSHIP"
	case strings.Contains(s, "temporary"), strings.Contains(s, "temp"):
		return "TEMPORARY"
	}
	return "UNKNOWN"
}

// ---- Salary ----

// Annualize converts a salary bound to a yearly figure in the same currency.
// No currency conversion is ever performed.
func Annualize(v *float64, period string) *float64 {
	if v == nil {
		return nil
	}
	factor := map[string]float64{"YEAR": 1, "MONTH": 12, "HOUR": 2080}[period]
	if factor == 0 {
		return nil
	}
	a := *v * factor
	return &a
}

// ---- Identity ----

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func norm(s string) string {
	s = strings.ToLower(strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return r
	}, s))
	return strings.Trim(nonAlnum.ReplaceAllString(s, " "), " ")
}

// DedupeKey identifies the same role across sources and reposts.
func DedupeKey(p Posting) string {
	loc := p.LocationRaw
	if i := strings.IndexAny(loc, ";|"); i > 0 {
		loc = loc[:i]
	}
	return norm(p.Company) + "|" + norm(p.Title) + "|" + norm(loc)
}

// ContentHash changes when user-visible content changes.
func ContentHash(p Posting) []byte {
	h := sha256.New()
	fmt.Fprintf(h, "v%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00", NormalizerVersion, p.Title, p.Company, p.LocationRaw, p.ApplyURL, p.DescriptionText)
	if p.Salary != nil {
		fmt.Fprintf(h, "%v%v%s%s", deref(p.Salary.Min), deref(p.Salary.Max), p.Salary.Currency, p.Salary.Period)
	}
	return h.Sum(nil)
}

func deref(v *float64) float64 {
	if v == nil {
		return -1
	}
	return *v
}

func parseTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			t = t.UTC()
			return &t
		}
	}
	return nil
}

func unixMillis(ms int64) *time.Time {
	if ms <= 0 {
		return nil
	}
	t := time.UnixMilli(ms).UTC()
	return &t
}

func trimAll(ss []string) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// IsHTTPURL reports whether s is an absolute http(s) URL.
func IsHTTPURL(s string) bool {
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")
}

// RepairMojibake undoes UTF-8 text that was decoded as Latin-1 and encoded
// again ("â\u0080\u0094" instead of "—"). It only rewrites strings made
// entirely of runes ≤ U+00FF whose byte form is valid UTF-8 containing
// multi-byte sequences, so correct text (including real accented Latin-1
// letters, which rarely form valid UTF-8 sequences) is left unchanged.
func RepairMojibake(s string) string {
	hasHigh := false
	b := make([]byte, 0, len(s))
	for _, r := range s {
		if r > 0xFF {
			return s
		}
		if r >= 0x80 {
			hasHigh = true
		}
		b = append(b, byte(r))
	}
	if !hasHigh || !utf8.Valid(b) {
		return s
	}
	return string(b)
}
