// Package listings exposes discovered job postings: search, detail, saved
// searches and dashboard counts. Postings are global; saved searches are
// user-owned.
package listings

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
)

// Filters are the validated search parameters (also stored in saved searches).
type Filters struct {
	Query          string   `json:"q,omitempty"`
	Remote         []string `json:"remote,omitempty"`  // REMOTE | HYBRID | ONSITE | UNKNOWN
	Country        string   `json:"country,omitempty"` // located in, or remote-eligible for
	Sources        []string `json:"source,omitempty"`  // source kinds
	Employment     []string `json:"employment,omitempty"`
	PostedWithin   int      `json:"posted_within,omitempty"` // days
	SalaryDisclose bool     `json:"salary_disclosed,omitempty"`
	MinSalary      float64  `json:"min_salary,omitempty"` // annualised, same currency only
	Currency       string   `json:"currency,omitempty"`
	Company        string   `json:"company,omitempty"`
	Sort           string   `json:"sort,omitempty"` // relevance | newest | salary
}

var (
	remoteVals     = []string{"REMOTE", "HYBRID", "ONSITE", "UNKNOWN"}
	sourceVals     = []string{"GREENHOUSE", "LEVER", "ASHBY", "REMOTEOK"}
	employmentVals = []string{"FULL_TIME", "PART_TIME", "CONTRACT", "INTERNSHIP", "TEMPORARY", "UNKNOWN"}
	isoRe          = regexp.MustCompile(`^[A-Z]{2}$`)
	currencyRe     = regexp.MustCompile(`^[A-Z]{3}$`)
)

func list(v url.Values, key string) []string {
	var out []string
	for _, raw := range v[key] {
		for _, s := range strings.Split(raw, ",") {
			if s = strings.ToUpper(strings.TrimSpace(s)); s != "" && !slices.Contains(out, s) {
				out = append(out, s)
			}
		}
	}
	return out
}

// ParseFilters validates query parameters. Unknown values are rejected (422)
// rather than silently ignored.
func ParseFilters(v url.Values) (Filters, error) {
	f := Filters{
		Query:      strings.TrimSpace(v.Get("q")),
		Remote:     list(v, "remote"),
		Country:    strings.ToUpper(strings.TrimSpace(v.Get("country"))),
		Sources:    list(v, "source"),
		Employment: list(v, "employment"),
		Currency:   strings.ToUpper(strings.TrimSpace(v.Get("currency"))),
		Company:    strings.TrimSpace(v.Get("company")),
		Sort:       strings.ToLower(strings.TrimSpace(v.Get("sort"))),
	}
	errs := map[string]string{}
	if len(f.Query) > 200 {
		errs["q"] = "At most 200 characters."
	}
	if len(f.Company) > 120 {
		errs["company"] = "At most 120 characters."
	}
	for key, vals := range map[string][]string{"remote": f.Remote, "source": f.Sources, "employment": f.Employment} {
		allowed := map[string][]string{"remote": remoteVals, "source": sourceVals, "employment": employmentVals}[key]
		for _, x := range vals {
			if !slices.Contains(allowed, x) {
				errs[key] = "Unsupported value " + strconv.Quote(x) + "."
			}
		}
	}
	if f.Country != "" && !isoRe.MatchString(f.Country) {
		errs["country"] = "Use a two-letter ISO country code."
	}
	if s := v.Get("posted_within"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 90 {
			errs["posted_within"] = "Between 1 and 90 days."
		}
		f.PostedWithin = n
	}
	if s := v.Get("salary_disclosed"); s != "" {
		b, err := strconv.ParseBool(s)
		if err != nil {
			errs["salary_disclosed"] = "Use true or false."
		}
		f.SalaryDisclose = b
	}
	if s := v.Get("min_salary"); s != "" {
		n, err := strconv.ParseFloat(s, 64)
		if err != nil || n < 0 || n > 1e8 {
			errs["min_salary"] = "Enter a positive annual amount."
		}
		f.MinSalary = n
		if !currencyRe.MatchString(f.Currency) {
			errs["currency"] = "A three-letter currency is required with a minimum salary."
		}
	}
	switch f.Sort {
	case "":
		f.Sort = "newest"
		if f.Query != "" {
			f.Sort = "relevance"
		}
	case "relevance", "newest", "salary", "match":
	default:
		errs["sort"] = "Use relevance, newest, salary or match."
	}
	if f.Sort == "relevance" && f.Query == "" {
		f.Sort = "newest"
	}
	if len(errs) > 0 {
		return f, httpx.Validation(errs)
	}
	return f, nil
}

// Summary is a job as shown in result lists.
type Summary struct {
	ID              uuid.UUID  `json:"id"`
	Title           string     `json:"title"`
	Company         string     `json:"company"`
	Department      string     `json:"department"`
	Location        string     `json:"location"`
	RemoteStatus    string     `json:"remote_status"`
	RemoteScope     string     `json:"remote_scope"`
	RemoteCountries []string   `json:"remote_countries"`
	RemoteRegion    string     `json:"remote_region"`
	Countries       []string   `json:"location_countries"`
	EmploymentType  string     `json:"employment_type"`
	Salary          *SalaryOut `json:"salary"`
	SalaryText      string     `json:"salary_text"`
	PostedAt        *time.Time `json:"posted_at"`
	FirstSeenAt     time.Time  `json:"first_seen_at"`
	Source          SourceRef  `json:"source"`
	ExpiredAt       *time.Time `json:"expired_at"`
	// Match is the viewer's stored match result, when one exists.
	Match *MatchBrief `json:"match"`
}

// MatchBrief summarises a stored match result for list views.
type MatchBrief struct {
	Score       float64 `json:"score"`
	Eligibility string  `json:"eligibility"`
	Confidence  string  `json:"confidence"`
}

// Viewer identifies whose match results to join into list results.
type Viewer struct {
	UserID       uuid.UUID
	MatchVersion string // empty: matching disabled
}

type SalaryOut struct {
	Min      *float64 `json:"min"`
	Max      *float64 `json:"max"`
	Currency string   `json:"currency"`
	Period   string   `json:"period"`
}

type SourceRef struct {
	Kind                string `json:"kind"`
	Name                string `json:"name"`
	AttributionRequired bool   `json:"attribution_required"`
}

type Page struct {
	Data       []Summary `json:"data"`
	NextCursor *string   `json:"next_cursor"`
	Total      int       `json:"total"`
}

// SummaryCols selects the columns ScanSummary reads (aliases j = jobs, s = job_sources).
const SummaryCols = `j.id, j.title, j.company_name, j.department, j.location_raw, j.remote_status, j.remote_scope,
	j.remote_countries, j.remote_region, j.location_countries, j.employment_type, j.salary_min::float8, j.salary_max::float8,
	j.salary_currency, j.salary_period, j.salary_text, j.posted_at, j.first_seen_at, s.kind, s.display_name, j.expired_at`

// ScanSummary reads SummaryCols, then any extra destinations.
func ScanSummary(r pgx.Row, extra ...any) (Summary, error) {
	var x Summary
	var smin, smax *float64
	var cur, per *string
	dst := []any{&x.ID, &x.Title, &x.Company, &x.Department, &x.Location, &x.RemoteStatus, &x.RemoteScope,
		&x.RemoteCountries, &x.RemoteRegion, &x.Countries, &x.EmploymentType, &smin, &smax, &cur, &per, &x.SalaryText,
		&x.PostedAt, &x.FirstSeenAt, &x.Source.Kind, &x.Source.Name, &x.ExpiredAt}
	if err := r.Scan(append(dst, extra...)...); err != nil {
		return x, err
	}
	if cur != nil && per != nil && (smin != nil || smax != nil) {
		x.Salary = &SalaryOut{Min: smin, Max: smax, Currency: *cur, Period: *per}
	}
	x.Source.AttributionRequired = x.Source.Kind == "REMOTEOK"
	return x, nil
}

type query struct {
	where []string
	args  []any
}

func (q *query) add(cond string, args ...any) {
	for _, a := range args {
		q.args = append(q.args, a)
		cond = strings.Replace(cond, "?", "$"+strconv.Itoa(len(q.args)), 1)
	}
	q.where = append(q.where, cond)
}

// MaxOffset bounds the offset cursor.
const MaxOffset = 2000

// EncodeCursor and DecodeCursor implement the opaque offset cursor (KI-020).
func EncodeCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte("o:" + strconv.Itoa(offset)))
}

func DecodeCursor(c string) (int, error) {
	if c == "" {
		return 0, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil || !strings.HasPrefix(string(b), "o:") {
		return 0, errors.New("bad cursor")
	}
	n, err := strconv.Atoi(strings.TrimPrefix(string(b), "o:"))
	if err != nil || n < 0 || n > MaxOffset {
		return 0, errors.New("bad cursor")
	}
	return n, nil
}

// Search returns active, canonical postings matching f. The cursor is opaque
// (currently an offset bounded at MaxOffset; see KNOWN_ISSUES).
func Search(ctx context.Context, q db.Querier, f Filters, limit int, cursor string, viewer Viewer) (Page, error) {
	offset, err := DecodeCursor(cursor)
	if err != nil {
		return Page{}, httpx.Validation(map[string]string{"cursor": "Invalid cursor."})
	}
	qb := &query{}
	qb.add("j.expired_at IS NULL")
	qb.add("j.duplicate_of IS NULL")
	rank := "0"
	if f.Query != "" {
		qb.add("j.search @@ websearch_to_tsquery('english', ?)", f.Query)
		rank = fmt.Sprintf("ts_rank(j.search, websearch_to_tsquery('english', $%d))", len(qb.args))
	}
	if len(f.Remote) > 0 {
		qb.add("j.remote_status = ANY(?)", f.Remote)
	}
	if f.Country != "" {
		qb.add("(j.location_countries @> ARRAY[?]::text[] OR j.remote_countries @> ARRAY[?]::text[] OR j.remote_scope = 'WORLDWIDE')", f.Country, f.Country)
	}
	if len(f.Sources) > 0 {
		qb.add("s.kind = ANY(?)", f.Sources)
	}
	if len(f.Employment) > 0 {
		qb.add("j.employment_type = ANY(?)", f.Employment)
	}
	if f.PostedWithin > 0 {
		qb.add("COALESCE(j.posted_at, j.first_seen_at) >= now() - make_interval(days => ?)", f.PostedWithin)
	}
	if f.SalaryDisclose {
		qb.add("(j.salary_min IS NOT NULL OR j.salary_max IS NOT NULL)")
	}
	if f.MinSalary > 0 {
		// Unknown salaries are kept unless the user asked for disclosed only;
		// different currencies are never compared (no FX conversion).
		qb.add("(j.salary_annual_max IS NULL AND j.salary_annual_min IS NULL OR (j.salary_currency = ? AND COALESCE(j.salary_annual_max, j.salary_annual_min) >= ?))", f.Currency, f.MinSalary)
	}
	if f.Company != "" {
		qb.add("j.company_name ILIKE ?", "%"+strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(f.Company)+"%")
	}
	where := strings.Join(qb.where, " AND ")
	order := "COALESCE(j.posted_at, j.first_seen_at) DESC, j.id"
	switch f.Sort {
	case "relevance":
		order = rank + " DESC, COALESCE(j.posted_at, j.first_seen_at) DESC, j.id"
	case "salary":
		order = "COALESCE(j.salary_annual_max, j.salary_annual_min) DESC NULLS LAST, COALESCE(j.posted_at, j.first_seen_at) DESC, j.id"
	case "match":
		// Ineligible jobs sink below everything scored; unscored jobs come last.
		order = "(m.eligibility = 'INELIGIBLE') NULLS LAST, m.overall_score DESC NULLS LAST, COALESCE(j.posted_at, j.first_seen_at) DESC, j.id"
	}
	matchJoin, matchCols := "", ", NULL::float8, NULL::text, NULL::text"
	if viewer.MatchVersion != "" {
		qb.args = append(qb.args, viewer.UserID, viewer.MatchVersion)
		matchJoin = fmt.Sprintf(" LEFT JOIN job_match_results m ON m.job_id = j.id AND m.user_id = $%d AND m.algorithm_version = $%d",
			len(qb.args)-1, len(qb.args))
		matchCols = ", m.overall_score::float8, m.eligibility, m.confidence"
	} else if f.Sort == "match" {
		return Page{}, httpx.Validation(map[string]string{"sort": "Matching is not enabled."})
	}

	var total int
	countArgs := qb.args
	if viewer.MatchVersion != "" {
		countArgs = qb.args[:len(qb.args)-2]
	}
	if err := q.QueryRow(ctx, `SELECT count(*) FROM jobs j JOIN job_sources s ON s.id = j.source_id WHERE `+where, countArgs...).Scan(&total); err != nil {
		return Page{}, err
	}
	args := append(qb.args, limit+1, offset)
	rows, err := q.Query(ctx, fmt.Sprintf(`SELECT %s%s FROM jobs j JOIN job_sources s ON s.id = j.source_id%s WHERE %s ORDER BY %s LIMIT $%d OFFSET $%d`,
		SummaryCols, matchCols, matchJoin, where, order, len(args)-1, len(args)), args...)
	if err != nil {
		return Page{}, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Summary, error) {
		var score *float64
		var elig, conf *string
		x, err := ScanSummary(r, &score, &elig, &conf)
		if err == nil && score != nil && elig != nil && conf != nil {
			x.Match = &MatchBrief{Score: *score, Eligibility: *elig, Confidence: *conf}
		}
		return x, err
	})
	if err != nil {
		return Page{}, err
	}
	page := Page{Data: items, Total: total}
	if len(items) > limit {
		page.Data = items[:limit]
		if next := offset + limit; next <= MaxOffset {
			c := EncodeCursor(next)
			page.NextCursor = &c
		}
	}
	return page, nil
}

// Detail adds the sanitised description and links.
type Detail struct {
	Summary
	DescriptionHTML string     `json:"description_html"`
	CanonicalURL    string     `json:"canonical_url"`
	ApplyURL        string     `json:"apply_url"`
	Tags            []string   `json:"tags"`
	DuplicateOf     *uuid.UUID `json:"duplicate_of"`
	RepostOf        *uuid.UUID `json:"repost_of"`
	LastSeenAt      time.Time  `json:"last_seen_at"`
}

var ErrNotFound = errors.New("job not found")

func Get(ctx context.Context, q db.Querier, id uuid.UUID) (Detail, error) {
	var d Detail
	row := q.QueryRow(ctx, `SELECT `+SummaryCols+`, j.description_html, j.canonical_url, j.apply_url, j.tags, j.duplicate_of, j.repost_of, j.last_seen_at
		FROM jobs j JOIN job_sources s ON s.id = j.source_id WHERE j.id = $1`, id)
	s, err := ScanSummary(row, &d.DescriptionHTML, &d.CanonicalURL, &d.ApplyURL, &d.Tags, &d.DuplicateOf, &d.RepostOf, &d.LastSeenAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, ErrNotFound
	}
	d.Summary = s
	return d, err
}
