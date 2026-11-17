package applications

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/careerpilot/careerpilot/services/api/internal/automation"
)

type JobBrief struct {
	ID        uuid.UUID  `json:"id"`
	Title     string     `json:"title"`
	Company   string     `json:"company"`
	Location  string     `json:"location"`
	ApplyURL  string     `json:"apply_url"`
	ExpiredAt *time.Time `json:"expired_at"`
}

type MatchBrief struct {
	Score       float64 `json:"score"`
	Eligibility string  `json:"eligibility"`
}

type Summary struct {
	ID              uuid.UUID   `json:"id"`
	Status          string      `json:"status"`
	Channel         *string     `json:"channel"`
	Job             JobBrief    `json:"job"`
	Match           *MatchBrief `json:"match"`
	ApprovedDocs    int         `json:"approved_documents"`
	PendingAttempt  bool        `json:"pending_attempt"`
	ApprovedAt      *time.Time  `json:"approved_at"`
	SubmittedAt     *time.Time  `json:"submitted_at"`
	CreatedAt       time.Time   `json:"created_at"`
	UpdatedAt       time.Time   `json:"updated_at"`
	StatusChangedAt time.Time   `json:"status_changed_at"`
}

const summarySQL = `SELECT a.id, a.status, a.channel, j.id, j.title, j.company_name, j.location_raw, j.apply_url, j.expired_at,
		r.overall_score, r.eligibility,
		(SELECT count(*) FROM application_documents d WHERE d.user_id = a.user_id AND d.job_id = a.job_id AND d.approved_version_id IS NOT NULL),
		EXISTS (SELECT 1 FROM submission_attempts s WHERE s.application_id = a.id AND s.status = 'PENDING'),
		a.approved_at, a.submitted_at, a.created_at, a.updated_at,
		(SELECT max(h.created_at) FROM application_status_history h WHERE h.application_id = a.id)
	FROM applications a
	JOIN jobs j ON j.id = a.job_id
	LEFT JOIN job_match_results r ON r.user_id = a.user_id AND r.job_id = a.job_id AND r.algorithm_version = $2
	WHERE a.user_id = $1`

func scanSummary(row pgx.Row) (Summary, error) {
	var s Summary
	var score *float64
	var elig *string
	err := row.Scan(&s.ID, &s.Status, &s.Channel, &s.Job.ID, &s.Job.Title, &s.Job.Company, &s.Job.Location, &s.Job.ApplyURL, &s.Job.ExpiredAt,
		&score, &elig, &s.ApprovedDocs, &s.PendingAttempt, &s.ApprovedAt, &s.SubmittedAt, &s.CreatedAt, &s.UpdatedAt, &s.StatusChangedAt)
	if score != nil && elig != nil {
		s.Match = &MatchBrief{Score: *score, Eligibility: *elig}
	}
	return s, err
}

type ListFilter struct {
	Status string
	Query  string
}

func (s *Service) List(ctx context.Context, userID uuid.UUID, f ListFilter) ([]Summary, error) {
	q := summarySQL + ` AND ($3 = '' OR a.status = $3)
		AND ($4 = '' OR j.title ILIKE '%' || $4 || '%' OR j.company_name ILIKE '%' || $4 || '%')
		ORDER BY a.updated_at DESC LIMIT 500`
	rows, err := s.pool.Query(ctx, q, userID, s.matchVersion, f.Status, escapeLike(f.Query))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Summary{}
	for rows.Next() {
		sm, err := scanSummary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sm)
	}
	return out, rows.Err()
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.TrimSpace(s))
}

type HistoryEntry struct {
	FromStatus *string   `json:"from_status"`
	ToStatus   string    `json:"to_status"`
	Actor      string    `json:"actor"`
	Reason     string    `json:"reason"`
	CreatedAt  time.Time `json:"created_at"`
}

type Evidence struct {
	ID         uuid.UUID  `json:"id"`
	AttemptID  uuid.UUID  `json:"attempt_id"`
	Type       string     `json:"type"`
	Reference  string     `json:"reference"`
	Note       string     `json:"note"`
	DocumentID *uuid.UUID `json:"document_id"`
	OccurredAt time.Time  `json:"occurred_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

type ChannelInfo struct {
	Channel   string `json:"channel"`
	Available bool   `json:"available"`
	Detail    string `json:"detail,omitempty"`
}

type Pins struct {
	CV          *uuid.UUID `json:"cv_version_id"`
	CoverLetter *uuid.UUID `json:"cover_letter_version_id"`
	Answers     *uuid.UUID `json:"answers_version_id"`
}

type Detail struct {
	Summary
	Notes          string              `json:"notes"`
	Pinned         Pins                `json:"pinned"`
	Package        Package             `json:"package"`
	AllowedTargets []string            `json:"allowed_transitions"`
	Channels       []ChannelInfo       `json:"channels"`
	History        []HistoryEntry      `json:"history"`
	Attempts       []Attempt           `json:"attempts"`
	Evidence       []Evidence          `json:"evidence"`
	Automation     automation.Settings `json:"automation"`
}

func (s *Service) Get(ctx context.Context, userID, id uuid.UUID) (Detail, error) {
	sm, err := scanSummary(s.pool.QueryRow(ctx, summarySQL+` AND a.id = $3`, userID, s.matchVersion, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, errNotFound
	}
	if err != nil {
		return Detail{}, err
	}
	d := Detail{Summary: sm, AllowedTargets: ManualTargets(sm.Status)}
	if err := s.pool.QueryRow(ctx, `SELECT notes, cv_version_id, cover_letter_version_id, answers_version_id FROM applications WHERE id = $1`, id).
		Scan(&d.Notes, &d.Pinned.CV, &d.Pinned.CoverLetter, &d.Pinned.Answers); err != nil {
		return Detail{}, err
	}
	if d.Package, err = loadPackage(ctx, s.pool, userID, sm.Job.ID); err != nil {
		return Detail{}, err
	}
	for _, ch := range []string{ChannelAssisted, ChannelEmail} {
		ok, detail := s.submitters[ch].Availability()
		d.Channels = append(d.Channels, ChannelInfo{Channel: ch, Available: ok, Detail: detail})
	}

	rows, err := s.pool.Query(ctx, `SELECT from_status, to_status, actor, reason, created_at FROM application_status_history
		WHERE application_id = $1 ORDER BY created_at, id`, id)
	if err != nil {
		return Detail{}, err
	}
	d.History, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (HistoryEntry, error) {
		var h HistoryEntry
		return h, r.Scan(&h.FromStatus, &h.ToStatus, &h.Actor, &h.Reason, &h.CreatedAt)
	})
	if err != nil {
		return Detail{}, err
	}
	rows, err = s.pool.Query(ctx, `SELECT `+attemptCols+` FROM submission_attempts WHERE application_id = $1 ORDER BY started_at DESC`, id)
	if err != nil {
		return Detail{}, err
	}
	d.Attempts, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Attempt, error) { return scanAttempt(r) })
	if err != nil {
		return Detail{}, err
	}
	rows, err = s.pool.Query(ctx, `SELECT id, attempt_id, type, reference, note, document_id, occurred_at, created_at
		FROM submission_evidence WHERE application_id = $1 ORDER BY created_at`, id)
	if err != nil {
		return Detail{}, err
	}
	d.Evidence, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Evidence, error) {
		var e Evidence
		return e, r.Scan(&e.ID, &e.AttemptID, &e.Type, &e.Reference, &e.Note, &e.DocumentID, &e.OccurredAt, &e.CreatedAt)
	})
	if err != nil {
		return Detail{}, err
	}
	d.Automation, err = automation.Load(ctx, s.pool, userID)
	return d, err
}

// ByJob returns the user's application for a job (or a duplicate of it).
func (s *Service) ByJob(ctx context.Context, userID, jobID uuid.UUID) (*Summary, error) {
	sm, err := scanSummary(s.pool.QueryRow(ctx, summarySQL+` AND (a.job_id = $3 OR coalesce(j.duplicate_of, j.id) =
		(SELECT coalesce(duplicate_of, id) FROM jobs WHERE id = $3)) LIMIT 1`, userID, s.matchVersion, jobID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sm, nil
}

// Stats are computed from stored applications and history only. Response
// metrics are null until there are submissions to measure.
type Stats struct {
	ByStatus         map[string]int `json:"by_status"`
	Active           int            `json:"active"`
	AwaitingApproval int            `json:"awaiting_approval"`
	Interviewing     int            `json:"interviewing"`
	Offers           int            `json:"offers"`
	Submitted        int            `json:"submitted"`
	ResponseRate     *float64       `json:"response_rate"`
	MedianDaysToResp *float64       `json:"median_days_to_response"`
	PausedAutomation bool           `json:"automation_paused"`
}

var responded = map[string]bool{Acknowledged: true, Assessment: true, Interviewing: true, Offer: true, Rejected: true}

func (s *Service) Stats(ctx context.Context, userID uuid.UUID) (Stats, error) {
	st := Stats{ByStatus: map[string]int{}}
	for _, k := range AllStatuses {
		st.ByStatus[k] = 0
	}
	rows, err := s.pool.Query(ctx, `SELECT status, count(*) FROM applications WHERE user_id = $1 GROUP BY 1`, userID)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			rows.Close()
			return st, err
		}
		st.ByStatus[k] = n
		if !IsTerminal(k) {
			st.Active += n
		}
	}
	rows.Close()
	st.AwaitingApproval = st.ByStatus[AwaitingApproval]
	st.Interviewing = st.ByStatus[Interviewing] + st.ByStatus[Assessment]
	st.Offers = st.ByStatus[Offer]

	// First employer response after submission, per submitted application.
	rows, err = s.pool.Query(ctx, `SELECT a.submitted_at,
			(SELECT min(h.created_at) FROM application_status_history h WHERE h.application_id = a.id
				AND h.to_status IN ('ACKNOWLEDGED','ASSESSMENT','INTERVIEWING','OFFER','REJECTED') AND h.created_at >= a.submitted_at)
		FROM applications a WHERE a.user_id = $1 AND a.submitted_at IS NOT NULL`, userID)
	if err != nil {
		return st, err
	}
	var days []float64
	for rows.Next() {
		var sub time.Time
		var first *time.Time
		if err := rows.Scan(&sub, &first); err != nil {
			rows.Close()
			return st, err
		}
		st.Submitted++
		if first != nil {
			days = append(days, first.Sub(sub).Hours()/24)
		}
	}
	rows.Close()
	if st.Submitted > 0 {
		r := float64(len(days)) / float64(st.Submitted)
		st.ResponseRate = &r
	}
	if len(days) > 0 {
		sort.Float64s(days)
		m := days[len(days)/2]
		if len(days)%2 == 0 {
			m = (days[len(days)/2-1] + days[len(days)/2]) / 2
		}
		st.MedianDaysToResp = &m
	}
	set, err := automation.Load(ctx, s.pool, userID)
	st.PausedAutomation = set.Paused
	return st, err
}

type Activity struct {
	ApplicationID uuid.UUID `json:"application_id"`
	JobTitle      string    `json:"job_title"`
	Company       string    `json:"company"`
	FromStatus    *string   `json:"from_status"`
	ToStatus      string    `json:"to_status"`
	Actor         string    `json:"actor"`
	Reason        string    `json:"reason"`
	CreatedAt     time.Time `json:"created_at"`
}

// Activity is the user's recent execution history across applications.
func (s *Service) Activity(ctx context.Context, userID uuid.UUID, limit int) ([]Activity, error) {
	rows, err := s.pool.Query(ctx, `SELECT h.application_id, j.title, j.company_name, h.from_status, h.to_status, h.actor, h.reason, h.created_at
		FROM application_status_history h JOIN applications a ON a.id = h.application_id JOIN jobs j ON j.id = a.job_id
		WHERE h.user_id = $1 ORDER BY h.created_at DESC, h.id LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Activity, error) {
		var a Activity
		return a, r.Scan(&a.ApplicationID, &a.JobTitle, &a.Company, &a.FromStatus, &a.ToStatus, &a.Actor, &a.Reason, &a.CreatedAt)
	})
	if out == nil {
		out = []Activity{}
	}
	return out, err
}
