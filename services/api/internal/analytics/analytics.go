// Package analytics computes career analytics from persisted records only
// (master prompt §13, ADR-025): no estimates, no vanity counts of automated
// actions. Rates carry their denominators and are flagged when the sample
// is too small to mean much.
package analytics

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	MinSample    = 5  // below this, a rate is shown but flagged
	matchedScore = 60 // "matches your criteria": score threshold and not ineligible
)

type Service struct {
	pool         *pgxpool.Pool
	matchVersion string
	now          func() time.Time
}

func NewService(pool *pgxpool.Pool, matchVersion string) *Service {
	return &Service{pool: pool, matchVersion: matchVersion, now: time.Now}
}

// Rate is a ratio with its basis.
type Rate struct {
	Numerator   int      `json:"numerator"`
	Denominator int      `json:"denominator"`
	Value       *float64 `json:"value"` // null when the denominator is 0
	LowSample   bool     `json:"low_sample"`
}

func rate(n, d int) Rate {
	r := Rate{Numerator: n, Denominator: d, LowSample: d < MinSample}
	if d > 0 {
		v := float64(n) / float64(d)
		r.Value = &v
	}
	return r
}

type Stage struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Count int    `json:"count"`
	// FromPrevious is the conversion from the previous stage.
	FromPrevious *Rate `json:"from_previous"`
}

type Funnel struct {
	Discovered int     `json:"jobs_discovered"`
	Matched    int     `json:"jobs_matched"`
	Stages     []Stage `json:"stages"`
	Rejected   int     `json:"rejected"`
	Withdrawn  int     `json:"withdrawn"`
}

type Duration struct {
	Key        string   `json:"key"`
	Label      string   `json:"label"`
	MedianDays *float64 `json:"median_days"`
	Samples    int      `json:"samples"`
}

type Outreach struct {
	Introductions int  `json:"introductions_sent"`
	FollowUps     int  `json:"follow_ups_sent"`
	Replied       int  `json:"introductions_replied"`
	Bounced       int  `json:"bounced"`
	ReplyRate     Rate `json:"reply_rate"`
}

type SourceStat struct {
	Source       string `json:"source"`
	Kind         string `json:"kind"`
	Matched      int    `json:"jobs_matched"`
	Applications int    `json:"applications"`
	Submitted    int    `json:"submitted"`
	Responses    int    `json:"responses"`
	Interviews   int    `json:"interviews"`
	ResponseRate Rate   `json:"response_rate"`
}

type SkillTrend struct {
	Skill string    `json:"skill"`
	Total int       `json:"total"`
	Share []float64 `json:"share"` // per week: share of strong matches missing this skill
}

type Week struct {
	Start        time.Time `json:"start"`
	Applications int       `json:"applications"`
	Submitted    int       `json:"submitted"`
	Responses    int       `json:"responses"`
	Outreach     int       `json:"outreach_sent"`
	Matches      int       `json:"strong_matches"`
}

type Overview struct {
	PeriodDays int          `json:"period_days"` // 0 = all time
	Since      *time.Time   `json:"since"`
	Funnel     Funnel       `json:"funnel"`
	Durations  []Duration   `json:"durations"`
	Outreach   Outreach     `json:"outreach"`
	Sources    []SourceStat `json:"sources"`
	SkillWeeks []time.Time  `json:"skill_weeks"`
	Skills     []SkillTrend `json:"skill_trends"`
	Weekly     []Week       `json:"weekly"`
	ComputedAt time.Time    `json:"computed_at"`
}

// Stage order: an application has reached a stage if it was ever in it or
// in any later stage.
var stageOrder = []struct{ key, label string }{
	{"SHORTLISTED", "Shortlisted"},
	{"PREPARING", "Preparing"},
	{"APPROVED", "Approved"},
	{"SUBMITTED", "Submitted (with evidence)"},
	{"RESPONDED", "Employer responded"},
	{"INTERVIEW", "Assessment or interview"},
	{"OFFER", "Offer"},
}

func rank(status string) int {
	switch status {
	case "DISCOVERED", "SHORTLISTED":
		return 0
	case "PREPARING", "AWAITING_APPROVAL":
		return 1
	case "APPROVED", "FAILED":
		return 2
	case "SUBMITTED":
		return 3
	case "ACKNOWLEDGED", "REJECTED":
		return 4
	case "ASSESSMENT", "INTERVIEWING":
		return 5
	case "OFFER":
		return 6
	}
	return -1
}

type appHistory struct {
	id, jobID        uuid.UUID
	status           string
	createdAt        time.Time
	submittedAt      *time.Time
	statuses         []string
	times            []time.Time
	source, kind     string
	reachedRank      int
	firstResponse    *time.Time
	firstInterview   *time.Time
	firstOffer       *time.Time
	firstPreparation *time.Time
}

func (s *Service) loadApps(ctx context.Context, userID uuid.UUID, since *time.Time) ([]appHistory, error) {
	rows, err := s.pool.Query(ctx, `SELECT a.id, a.job_id, a.status, a.created_at, a.submitted_at, src.display_name, src.kind,
			array_agg(h.to_status ORDER BY h.created_at), array_agg(h.created_at ORDER BY h.created_at)
		FROM applications a JOIN jobs j ON j.id = a.job_id JOIN job_sources src ON src.id = j.source_id
		JOIN application_status_history h ON h.application_id = a.id
		WHERE a.user_id = $1 AND ($2::timestamptz IS NULL OR a.created_at >= $2)
		GROUP BY a.id, src.display_name, src.kind`, userID, since)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (appHistory, error) {
		var a appHistory
		if err := r.Scan(&a.id, &a.jobID, &a.status, &a.createdAt, &a.submittedAt, &a.source, &a.kind, &a.statuses, &a.times); err != nil {
			return a, err
		}
		a.reachedRank = 0
		for i, st := range a.statuses {
			if rk := rank(st); rk > a.reachedRank {
				a.reachedRank = rk
			}
			t := a.times[i]
			switch {
			case st == "PREPARING" && a.firstPreparation == nil:
				a.firstPreparation = &t
			case (st == "ACKNOWLEDGED" || st == "REJECTED" || st == "ASSESSMENT" || st == "INTERVIEWING" || st == "OFFER") && a.firstResponse == nil &&
				a.submittedAt != nil:
				a.firstResponse = &t
			}
			if (st == "ASSESSMENT" || st == "INTERVIEWING") && a.firstInterview == nil {
				a.firstInterview = &t
			}
			if st == "OFFER" && a.firstOffer == nil {
				a.firstOffer = &t
			}
		}
		// A response (rank 4+) only counts after an evidence-backed submission.
		if a.submittedAt == nil && a.reachedRank > 3 {
			a.reachedRank = 2
		}
		return a, nil
	})
}

func median(xs []float64) *float64 {
	if len(xs) == 0 {
		return nil
	}
	sort.Float64s(xs)
	m := xs[len(xs)/2]
	if len(xs)%2 == 0 {
		m = (xs[len(xs)/2-1] + xs[len(xs)/2]) / 2
	}
	return &m
}

func days(a, b time.Time) float64 { return b.Sub(a).Hours() / 24 }

// Overview computes analytics for the last `period` days (0 = all time).
func (s *Service) Overview(ctx context.Context, userID uuid.UUID, period int) (Overview, error) {
	now := s.now()
	o := Overview{PeriodDays: period, ComputedAt: now, Sources: []SourceStat{}, Skills: []SkillTrend{}, Weekly: []Week{}, SkillWeeks: []time.Time{}}
	var since *time.Time
	if period > 0 {
		t := now.AddDate(0, 0, -period)
		since = &t
		o.Since = &t
	}

	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE duplicate_of IS NULL AND ($1::timestamptz IS NULL OR first_seen_at >= $1)`, since).
		Scan(&o.Funnel.Discovered); err != nil {
		return o, err
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM job_match_results r JOIN jobs j ON j.id = r.job_id
		WHERE r.user_id = $1 AND r.algorithm_version = $2 AND r.overall_score >= $3 AND r.eligibility <> 'INELIGIBLE'
		AND ($4::timestamptz IS NULL OR j.first_seen_at >= $4)`, userID, s.matchVersion, matchedScore, since).Scan(&o.Funnel.Matched); err != nil {
		return o, err
	}

	apps, err := s.loadApps(ctx, userID, since)
	if err != nil {
		return o, err
	}
	counts := make([]int, len(stageOrder))
	for _, a := range apps {
		for i := 0; i <= a.reachedRank && i < len(counts); i++ {
			counts[i]++
		}
		switch a.status {
		case "REJECTED":
			o.Funnel.Rejected++
		case "WITHDRAWN":
			o.Funnel.Withdrawn++
		}
	}
	for i, st := range stageOrder {
		stage := Stage{Key: st.key, Label: st.label, Count: counts[i]}
		if i > 0 {
			r := rate(counts[i], counts[i-1])
			stage.FromPrevious = &r
		}
		o.Funnel.Stages = append(o.Funnel.Stages, stage)
	}

	// Stage durations over all of the user's applications (more samples).
	all, err := s.loadApps(ctx, userID, nil)
	if err != nil {
		return o, err
	}
	var toSubmit, toResponse, toInterview, toOffer []float64
	for _, a := range all {
		if a.submittedAt != nil {
			toSubmit = append(toSubmit, days(a.createdAt, *a.submittedAt))
			if a.firstResponse != nil {
				toResponse = append(toResponse, days(*a.submittedAt, *a.firstResponse))
			}
			if a.firstInterview != nil {
				toInterview = append(toInterview, days(*a.submittedAt, *a.firstInterview))
			}
			if a.firstOffer != nil {
				toOffer = append(toOffer, days(*a.submittedAt, *a.firstOffer))
			}
		}
	}
	o.Durations = []Duration{
		{"SHORTLIST_TO_SUBMIT", "Shortlisted → submitted", median(toSubmit), len(toSubmit)},
		{"SUBMIT_TO_RESPONSE", "Submitted → first response", median(toResponse), len(toResponse)},
		{"SUBMIT_TO_INTERVIEW", "Submitted → interview", median(toInterview), len(toInterview)},
		{"SUBMIT_TO_OFFER", "Submitted → offer", median(toOffer), len(toOffer)},
	}

	if err := s.pool.QueryRow(ctx, `SELECT
			count(*) FILTER (WHERE kind = 'INTRODUCTION'),
			count(*) FILTER (WHERE kind = 'FOLLOW_UP'),
			count(*) FILTER (WHERE kind = 'INTRODUCTION' AND replied_at IS NOT NULL),
			(SELECT count(*) FROM outreach_replies r WHERE r.user_id = $1 AND r.is_bounce AND ($2::timestamptz IS NULL OR r.received_at >= $2))
		FROM outreach_messages WHERE user_id = $1 AND status = 'SENT' AND ($2::timestamptz IS NULL OR sent_at >= $2)`, userID, since).
		Scan(&o.Outreach.Introductions, &o.Outreach.FollowUps, &o.Outreach.Replied, &o.Outreach.Bounced); err != nil {
		return o, err
	}
	o.Outreach.ReplyRate = rate(o.Outreach.Replied, o.Outreach.Introductions-o.Outreach.Bounced)

	if err := s.sources(ctx, userID, since, apps, &o); err != nil {
		return o, err
	}
	if err := s.skillTrends(ctx, userID, now, &o); err != nil {
		return o, err
	}
	if err := s.weekly(ctx, userID, now, all, &o); err != nil {
		return o, err
	}
	return o, nil
}

func (s *Service) sources(ctx context.Context, userID uuid.UUID, since *time.Time, apps []appHistory, o *Overview) error {
	byName := map[string]*SourceStat{}
	get := func(name, kind string) *SourceStat {
		if st, ok := byName[name]; ok {
			return st
		}
		st := &SourceStat{Source: name, Kind: kind}
		byName[name] = st
		return st
	}
	rows, err := s.pool.Query(ctx, `SELECT src.display_name, src.kind, count(*) FROM job_match_results r JOIN jobs j ON j.id = r.job_id
		JOIN job_sources src ON src.id = j.source_id
		WHERE r.user_id = $1 AND r.algorithm_version = $2 AND r.overall_score >= $3 AND r.eligibility <> 'INELIGIBLE'
		AND ($4::timestamptz IS NULL OR j.first_seen_at >= $4) GROUP BY 1, 2`, userID, s.matchVersion, matchedScore, since)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name, kind string
		var n int
		if err := rows.Scan(&name, &kind, &n); err != nil {
			rows.Close()
			return err
		}
		get(name, kind).Matched = n
	}
	rows.Close()
	for _, a := range apps {
		st := get(a.source, a.kind)
		st.Applications++
		if a.submittedAt != nil {
			st.Submitted++
			if a.firstResponse != nil {
				st.Responses++
			}
			if a.firstInterview != nil {
				st.Interviews++
			}
		}
	}
	for _, st := range byName {
		st.ResponseRate = rate(st.Responses, st.Submitted)
		o.Sources = append(o.Sources, *st)
	}
	slices.SortFunc(o.Sources, func(a, b SourceStat) int {
		if a.Submitted != b.Submitted {
			return b.Submitted - a.Submitted
		}
		if a.Matched != b.Matched {
			return b.Matched - a.Matched
		}
		if a.Source < b.Source {
			return -1
		}
		return 1
	})
	if len(o.Sources) > 15 {
		o.Sources = o.Sources[:15]
	}
	return nil
}

// skillTrends: for the last 8 weeks (by when jobs were first seen), the share
// of the user's strong matches that list each top missing skill.
func (s *Service) skillTrends(ctx context.Context, userID uuid.UUID, now time.Time, o *Overview) error {
	start := weekStart(now).AddDate(0, 0, -7*7)
	for i := 0; i < 8; i++ {
		o.SkillWeeks = append(o.SkillWeeks, start.AddDate(0, 0, 7*i))
	}
	matches := make([]int, 8)
	rows, err := s.pool.Query(ctx, `SELECT j.first_seen_at, r.result->'missing_skills' FROM job_match_results r JOIN jobs j ON j.id = r.job_id
		WHERE r.user_id = $1 AND r.algorithm_version = $2 AND r.overall_score >= 50 AND r.eligibility <> 'INELIGIBLE' AND j.first_seen_at >= $3`,
		userID, s.matchVersion, start)
	if err != nil {
		return err
	}
	counts := map[string][]int{}
	totals := map[string]int{}
	for rows.Next() {
		var seen time.Time
		var missing []string
		if err := rows.Scan(&seen, &missing); err != nil {
			rows.Close()
			return err
		}
		w := int(weekStart(seen).Sub(start).Hours() / (24 * 7))
		if w < 0 || w > 7 {
			continue
		}
		matches[w]++
		for _, sk := range missing {
			if counts[sk] == nil {
				counts[sk] = make([]int, 8)
			}
			counts[sk][w]++
			totals[sk]++
		}
	}
	rows.Close()
	names := make([]string, 0, len(totals))
	for k := range totals {
		names = append(names, k)
	}
	slices.SortFunc(names, func(a, b string) int {
		if totals[a] != totals[b] {
			return totals[b] - totals[a]
		}
		if a < b {
			return -1
		}
		return 1
	})
	if len(names) > 5 {
		names = names[:5]
	}
	for _, n := range names {
		t := SkillTrend{Skill: n, Total: totals[n], Share: make([]float64, 8)}
		for w := range 8 {
			if matches[w] > 0 {
				t.Share[w] = float64(counts[n][w]) / float64(matches[w])
			}
		}
		o.Skills = append(o.Skills, t)
	}
	return nil
}

// weekStart is Monday 00:00 UTC of t's week.
func weekStart(t time.Time) time.Time {
	t = t.UTC()
	d := (int(t.Weekday()) + 6) % 7
	return time.Date(t.Year(), t.Month(), t.Day()-d, 0, 0, 0, 0, time.UTC)
}

func (s *Service) weekly(ctx context.Context, userID uuid.UUID, now time.Time, apps []appHistory, o *Overview) error {
	start := weekStart(now).AddDate(0, 0, -7*11)
	weeks := make([]Week, 12)
	idx := func(t time.Time) int {
		w := int(weekStart(t).Sub(start).Hours() / (24 * 7))
		if w < 0 || w > 11 {
			return -1
		}
		return w
	}
	for i := range weeks {
		weeks[i].Start = start.AddDate(0, 0, 7*i)
	}
	for _, a := range apps {
		if i := idx(a.createdAt); i >= 0 {
			weeks[i].Applications++
		}
		if a.submittedAt != nil {
			if i := idx(*a.submittedAt); i >= 0 {
				weeks[i].Submitted++
			}
		}
		if a.firstResponse != nil {
			if i := idx(*a.firstResponse); i >= 0 {
				weeks[i].Responses++
			}
		}
	}
	rows, err := s.pool.Query(ctx, `SELECT sent_at FROM outreach_messages WHERE user_id = $1 AND status = 'SENT' AND sent_at >= $2`, userID, start)
	if err != nil {
		return err
	}
	for rows.Next() {
		var t time.Time
		if err := rows.Scan(&t); err != nil {
			rows.Close()
			return err
		}
		if i := idx(t); i >= 0 {
			weeks[i].Outreach++
		}
	}
	rows.Close()
	rows, err = s.pool.Query(ctx, `SELECT j.first_seen_at FROM job_match_results r JOIN jobs j ON j.id = r.job_id
		WHERE r.user_id = $1 AND r.algorithm_version = $2 AND r.overall_score >= $3 AND r.eligibility <> 'INELIGIBLE' AND j.first_seen_at >= $4`,
		userID, s.matchVersion, matchedScore, start)
	if err != nil {
		return err
	}
	for rows.Next() {
		var t time.Time
		if err := rows.Scan(&t); err != nil {
			rows.Close()
			return err
		}
		if i := idx(t); i >= 0 {
			weeks[i].Matches++
		}
	}
	rows.Close()
	o.Weekly = weeks
	return nil
}
