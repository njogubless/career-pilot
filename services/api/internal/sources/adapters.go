package sources

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Registry maps a kind to its adapter.
type Registry map[Kind]Adapter

func NewRegistry(f *Fetcher) Registry {
	return Registry{
		Greenhouse: &greenhouseAdapter{f: f, base: "https://boards-api.greenhouse.io"},
		Lever:      &leverAdapter{f: f, base: "https://api.lever.co"},
		Ashby:      &ashbyAdapter{f: f, base: "https://api.ashbyhq.com"},
		RemoteOK:   &remoteOKAdapter{f: f, base: "https://remoteok.com"},
	}
}

func checkID(id string) error {
	if !ValidIdentifier(id) {
		return &SourceError{Permanent: true, Msg: "invalid board identifier"}
	}
	return nil
}

// ---------------- Greenhouse ----------------
// GET /v1/boards/{token}/jobs?content=true  (public job board API)

type greenhouseAdapter struct {
	f    *Fetcher
	base string
}

func (*greenhouseAdapter) Kind() Kind { return Greenhouse }

type ghResponse struct {
	Jobs []struct {
		ID             int64  `json:"id"`
		Title          string `json:"title"`
		AbsoluteURL    string `json:"absolute_url"`
		CompanyName    string `json:"company_name"`
		UpdatedAt      string `json:"updated_at"`
		FirstPublished string `json:"first_published"`
		Content        string `json:"content"` // HTML-escaped HTML
		Location       struct {
			Name string `json:"name"`
		} `json:"location"`
		Departments []struct {
			Name string `json:"name"`
		} `json:"departments"`
		Offices []struct {
			Name string `json:"name"`
		} `json:"offices"`
	} `json:"jobs"`
}

func (a *greenhouseAdapter) Fetch(ctx context.Context, token string) ([]Posting, error) {
	if err := checkID(token); err != nil {
		return nil, err
	}
	var r ghResponse
	if err := a.f.GetJSON(ctx, fmt.Sprintf("%s/v1/boards/%s/jobs?content=true", a.base, url.PathEscape(token)), &r); err != nil {
		return nil, err
	}
	out := make([]Posting, 0, len(r.Jobs))
	for _, j := range r.Jobs {
		raw := html.UnescapeString(j.Content)
		loc := strings.TrimSpace(j.Location.Name)
		countries := CountriesIn(loc)
		for _, o := range j.Offices {
			for _, c := range CountriesIn(o.Name) {
				if !contains(countries, c) {
					countries = append(countries, c)
				}
			}
		}
		status, scope, rc, region := ClassifyLocation(loc, "")
		p := Posting{
			ExternalID: strconv.FormatInt(j.ID, 10), CanonicalURL: j.AbsoluteURL, ApplyURL: j.AbsoluteURL,
			Title: strings.TrimSpace(j.Title), Company: strings.TrimSpace(j.CompanyName), LocationRaw: loc,
			LocationCountries: countries, RemoteStatus: status, RemoteScope: scope, RemoteCountries: rc, RemoteRegion: region,
			EmploymentType: "UNKNOWN", DescriptionHTML: SanitizeHTML(raw), DescriptionText: HTMLToText(raw),
			PostedAt: firstTime(j.FirstPublished, j.UpdatedAt),
		}
		if len(j.Departments) > 0 {
			p.Department = j.Departments[0].Name
		}
		if p.Company == "" {
			p.Company = token
		}
		out = append(out, p)
	}
	return out, nil
}

// ---------------- Lever ----------------
// GET /v0/postings/{company}?mode=json

type leverAdapter struct {
	f    *Fetcher
	base string
}

func (*leverAdapter) Kind() Kind { return Lever }

type leverPosting struct {
	ID          string `json:"id"`
	Text        string `json:"text"`
	HostedURL   string `json:"hostedUrl"`
	ApplyURL    string `json:"applyUrl"`
	CreatedAt   int64  `json:"createdAt"`
	Country     string `json:"country"`
	Workplace   string `json:"workplaceType"`
	Description string `json:"description"`
	Additional  string `json:"additional"`
	Lists       []struct {
		Text    string `json:"text"`
		Content string `json:"content"`
	} `json:"lists"`
	Categories struct {
		Commitment   string   `json:"commitment"`
		Location     string   `json:"location"`
		Team         string   `json:"team"`
		AllLocations []string `json:"allLocations"`
	} `json:"categories"`
	SalaryRange *struct {
		Min      *float64 `json:"min"`
		Max      *float64 `json:"max"`
		Currency string   `json:"currency"`
		Interval string   `json:"interval"`
	} `json:"salaryRange"`
}

func (a *leverAdapter) Fetch(ctx context.Context, company string) ([]Posting, error) {
	if err := checkID(company); err != nil {
		return nil, err
	}
	var r []leverPosting
	if err := a.f.GetJSON(ctx, fmt.Sprintf("%s/v0/postings/%s?mode=json", a.base, url.PathEscape(company)), &r); err != nil {
		return nil, err
	}
	out := make([]Posting, 0, len(r))
	for _, j := range r {
		var b strings.Builder
		b.WriteString(j.Description)
		for _, l := range j.Lists {
			fmt.Fprintf(&b, "<h3>%s</h3><ul>%s</ul>", html.EscapeString(l.Text), l.Content)
		}
		b.WriteString(j.Additional)
		raw := b.String()

		loc := j.Categories.Location
		if len(j.Categories.AllLocations) > 1 {
			loc = strings.Join(j.Categories.AllLocations, "; ")
		}
		countries := CountriesIn(loc)
		if j.Country != "" && len(j.Country) == 2 && !contains(countries, strings.ToUpper(j.Country)) {
			countries = append(countries, strings.ToUpper(j.Country))
		}
		status, scope, rc, region := ClassifyLocation(loc, j.Workplace)
		p := Posting{
			ExternalID: j.ID, CanonicalURL: j.HostedURL, ApplyURL: firstNonEmpty(j.ApplyURL, j.HostedURL),
			Title: strings.TrimSpace(j.Text), Company: company, Department: j.Categories.Team, LocationRaw: loc,
			LocationCountries: countries, RemoteStatus: status, RemoteScope: scope, RemoteCountries: rc, RemoteRegion: region,
			EmploymentType: EmploymentType(j.Categories.Commitment), DescriptionHTML: SanitizeHTML(raw), DescriptionText: HTMLToText(raw),
			PostedAt: unixMillis(j.CreatedAt),
		}
		if s := j.SalaryRange; s != nil && (s.Min != nil || s.Max != nil) && len(s.Currency) == 3 {
			if period := leverInterval(s.Interval); period != "" {
				p.Salary = &Salary{Min: s.Min, Max: s.Max, Currency: strings.ToUpper(s.Currency), Period: period}
			}
		}
		out = append(out, p)
	}
	return out, nil
}

func leverInterval(s string) string {
	switch strings.ToLower(s) {
	case "per-year-salary", "per-year", "yearly", "annual":
		return "YEAR"
	case "per-month-salary", "per-month", "monthly":
		return "MONTH"
	case "per-hour-wage", "per-hour", "hourly":
		return "HOUR"
	}
	return ""
}

// ---------------- Ashby ----------------
// GET /posting-api/job-board/{name}?includeCompensation=true

type ashbyAdapter struct {
	f    *Fetcher
	base string
}

func (*ashbyAdapter) Kind() Kind { return Ashby }

type ashbyResponse struct {
	Jobs []struct {
		ID                 string `json:"id"`
		Title              string `json:"title"`
		Department         string `json:"department"`
		EmploymentType     string `json:"employmentType"`
		Location           string `json:"location"`
		SecondaryLocations []struct {
			Location string `json:"location"`
		} `json:"secondaryLocations"`
		PublishedAt   string `json:"publishedAt"`
		IsListed      *bool  `json:"isListed"`
		IsRemote      bool   `json:"isRemote"`
		WorkplaceType string `json:"workplaceType"`
		JobURL        string `json:"jobUrl"`
		ApplyURL      string `json:"applyUrl"`
		DescHTML      string `json:"descriptionHtml"`
		Address       *struct {
			PostalAddress struct {
				AddressCountry string `json:"addressCountry"`
			} `json:"postalAddress"`
		} `json:"address"`
		Compensation *struct {
			Summary           string `json:"compensationTierSummary"`
			SummaryComponents []struct {
				Type     string   `json:"compensationType"`
				Interval string   `json:"interval"`
				Currency string   `json:"currencyCode"`
				Min      *float64 `json:"minValue"`
				Max      *float64 `json:"maxValue"`
			} `json:"summaryComponents"`
		} `json:"compensation"`
	} `json:"jobs"`
}

func (a *ashbyAdapter) Fetch(ctx context.Context, board string) ([]Posting, error) {
	if err := checkID(board); err != nil {
		return nil, err
	}
	var r ashbyResponse
	if err := a.f.GetJSON(ctx, fmt.Sprintf("%s/posting-api/job-board/%s?includeCompensation=true", a.base, url.PathEscape(board)), &r); err != nil {
		return nil, err
	}
	out := make([]Posting, 0, len(r.Jobs))
	for _, j := range r.Jobs {
		if j.IsListed != nil && !*j.IsListed {
			continue
		}
		locs := []string{j.Location}
		for _, s := range j.SecondaryLocations {
			locs = append(locs, s.Location)
		}
		loc := strings.Join(trimAll(locs), "; ")
		countries := CountriesIn(loc)
		if j.Address != nil {
			for _, c := range CountriesIn(j.Address.PostalAddress.AddressCountry) {
				if !contains(countries, c) {
					countries = append(countries, c)
				}
			}
		}
		workplace := j.WorkplaceType
		if workplace == "" && j.IsRemote {
			workplace = "remote"
		}
		status, scope, rc, region := ClassifyLocation(loc, workplace)
		p := Posting{
			ExternalID: j.ID, CanonicalURL: j.JobURL, ApplyURL: firstNonEmpty(j.ApplyURL, j.JobURL),
			Title: strings.TrimSpace(j.Title), Company: board, Department: j.Department, LocationRaw: loc,
			LocationCountries: countries, RemoteStatus: status, RemoteScope: scope, RemoteCountries: rc, RemoteRegion: region,
			EmploymentType: EmploymentType(j.EmploymentType), DescriptionHTML: SanitizeHTML(j.DescHTML), DescriptionText: HTMLToText(j.DescHTML),
			PostedAt: parseTime(j.PublishedAt),
		}
		if c := j.Compensation; c != nil {
			p.SalaryText = c.Summary
			for _, comp := range c.SummaryComponents {
				if strings.EqualFold(comp.Type, "Salary") && len(comp.Currency) == 3 && (comp.Min != nil || comp.Max != nil) {
					if period := ashbyInterval(comp.Interval); period != "" {
						p.Salary = &Salary{Min: comp.Min, Max: comp.Max, Currency: strings.ToUpper(comp.Currency), Period: period}
						break
					}
				}
			}
		}
		out = append(out, p)
	}
	return out, nil
}

func ashbyInterval(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "1 YEAR":
		return "YEAR"
	case "1 MONTH":
		return "MONTH"
	case "1 HOUR":
		return "HOUR"
	}
	return ""
}

// ---------------- Remote OK ----------------
// GET /api. The first array element is a legal notice; terms require a
// followed link back and naming Remote OK as the source (shown in the UI).

type remoteOKAdapter struct {
	f    *Fetcher
	base string
}

func (*remoteOKAdapter) Kind() Kind { return RemoteOK }

type remoteOKJob struct {
	ID          string   `json:"id"`
	Date        string   `json:"date"`
	Company     string   `json:"company"`
	Position    string   `json:"position"`
	Tags        []string `json:"tags"`
	Description string   `json:"description"`
	Location    string   `json:"location"`
	ApplyURL    string   `json:"apply_url"`
	URL         string   `json:"url"`
	SalaryMin   float64  `json:"salary_min"`
	SalaryMax   float64  `json:"salary_max"`
	Legal       string   `json:"legal"`
}

func (a *remoteOKAdapter) Fetch(ctx context.Context, _ string) ([]Posting, error) {
	var r []remoteOKJob
	if err := a.f.GetJSON(ctx, a.base+"/api", &r); err != nil {
		return nil, err
	}
	out := make([]Posting, 0, len(r))
	for _, j := range r {
		if j.Legal != "" || j.ID == "" || j.Position == "" {
			continue
		}
		loc := strings.TrimSpace(j.Location)
		// Every Remote OK listing is remote by definition; the location field
		// states where applicants may be (or is empty / a city).
		_, scope, rc, region := ClassifyLocation("remote "+loc, "remote")
		if loc == "" {
			scope = ScopeUnknown
		}
		p := Posting{
			ExternalID: j.ID, CanonicalURL: j.URL, ApplyURL: firstNonEmpty(j.ApplyURL, j.URL),
			Title: strings.TrimSpace(html.UnescapeString(j.Position)), Company: strings.TrimSpace(html.UnescapeString(j.Company)),
			LocationRaw: loc, LocationCountries: CountriesIn(loc), RemoteStatus: "REMOTE", RemoteScope: scope, RemoteCountries: rc,
			RemoteRegion: region, EmploymentType: "UNKNOWN", DescriptionHTML: SanitizeHTML(j.Description),
			DescriptionText: HTMLToText(j.Description), Tags: trimAll(j.Tags), PostedAt: parseTime(j.Date),
		}
		// Remote OK publishes salary_min/max in USD per year; 0 means not
		// disclosed. Values below 1,000/yr are implausible (observed in live
		// data) and are treated as undisclosed rather than shown as fact.
		if max(j.SalaryMin, j.SalaryMax) >= 1000 {
			s := &Salary{Currency: "USD", Period: "YEAR"}
			if j.SalaryMin >= 1000 {
				v := j.SalaryMin
				s.Min = &v
			}
			if j.SalaryMax >= 1000 {
				v := j.SalaryMax
				s.Max = &v
			}
			p.Salary = s
		}
		out = append(out, p)
	}
	return out, nil
}

// ---------------- helpers ----------------

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func firstTime(ss ...string) *time.Time {
	for _, s := range ss {
		if t := parseTime(s); t != nil {
			return t
		}
	}
	return nil
}
