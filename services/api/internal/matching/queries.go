package matching

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/careerpilot/careerpilot/services/api/internal/listings"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
)

// State is the user's matching progress.
type State struct {
	Status           string     `json:"status"` // NOT_STARTED | QUEUED | RUNNING | READY | FAILED
	AlgorithmVersion string     `json:"algorithm_version"`
	Candidates       int        `json:"candidates"`
	Scored           int        `json:"scored"`
	Error            *string    `json:"error"`
	StartedAt        *time.Time `json:"started_at"`
	CompletedAt      *time.Time `json:"completed_at"`
}

func GetState(ctx context.Context, q db.Querier, userID uuid.UUID) (State, error) {
	st := State{Status: "NOT_STARTED", AlgorithmVersion: AlgorithmVersion}
	var ver *string
	err := q.QueryRow(ctx, `SELECT status, algorithm_version, candidates, scored, error, started_at, completed_at
		FROM match_states WHERE user_id = $1`, userID).
		Scan(&st.Status, &ver, &st.Candidates, &st.Scored, &st.Error, &st.StartedAt, &st.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, nil
	}
	if ver != nil && *ver != AlgorithmVersion && st.Status == "READY" {
		st.Status = "QUEUED" // results from an older algorithm: a recompute is due
	}
	return st, err
}

// MatchItem is one ranked match in list views.
type MatchItem struct {
	Job           listings.Summary `json:"job"`
	Score         float64          `json:"score"`
	Eligibility   string           `json:"eligibility"`
	Confidence    string           `json:"confidence"`
	MissingSkills []string         `json:"missing_skills"`
	Signals       []Signal         `json:"signals"`
	Blockers      []string         `json:"blockers"`
	Feedback      *string          `json:"feedback"` // UP | DOWN
	ComputedAt    time.Time        `json:"computed_at"`
}

type MatchPage struct {
	Data       []MatchItem `json:"data"`
	NextCursor *string     `json:"next_cursor"`
	Total      int         `json:"total"`
	State      State       `json:"state"`
}

type ListFilter struct {
	Eligibility      string // ELIGIBLE | UNKNOWN | INELIGIBLE | "" (all except INELIGIBLE) | ALL
	MinScore         float64
	IncludeDismissed bool
}

func ListMatches(ctx context.Context, q db.Querier, userID uuid.UUID, f ListFilter, limit int, cursor string) (MatchPage, error) {
	offset, err := listings.DecodeCursor(cursor)
	if err != nil {
		return MatchPage{}, err
	}
	where := []string{"m.user_id = $1", "m.algorithm_version = $2", "j.expired_at IS NULL", "j.duplicate_of IS NULL", "m.overall_score >= $3"}
	args := []any{userID, AlgorithmVersion, f.MinScore}
	switch f.Eligibility {
	case "":
		where = append(where, "m.eligibility <> 'INELIGIBLE'")
	case "ALL":
	default:
		args = append(args, f.Eligibility)
		where = append(where, fmt.Sprintf("m.eligibility = $%d", len(args)))
	}
	if !f.IncludeDismissed {
		where = append(where, "NOT COALESCE(fb.dismissed, false)")
	}
	from := `FROM job_match_results m JOIN jobs j ON j.id = m.job_id JOIN job_sources s ON s.id = j.source_id
		LEFT JOIN match_feedback fb ON fb.user_id = m.user_id AND fb.job_id = m.job_id WHERE ` + strings.Join(where, " AND ")
	var total int
	if err := q.QueryRow(ctx, "SELECT count(*) "+from, args...).Scan(&total); err != nil {
		return MatchPage{}, err
	}
	args = append(args, limit+1, offset)
	rows, err := q.Query(ctx, fmt.Sprintf(`SELECT %s, m.overall_score::float8, m.eligibility, m.confidence, m.result, fb.rating, m.computed_at %s
		ORDER BY m.overall_score DESC, j.id LIMIT $%d OFFSET $%d`, listings.SummaryCols, from, len(args)-1, len(args)), args...)
	if err != nil {
		return MatchPage{}, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (MatchItem, error) {
		var it MatchItem
		var raw []byte
		var err error
		it.Job, err = listings.ScanSummary(r, &it.Score, &it.Eligibility, &it.Confidence, &raw, &it.Feedback, &it.ComputedAt)
		if err != nil {
			return it, err
		}
		var res Result
		if err := json.Unmarshal(raw, &res); err != nil {
			return it, err
		}
		it.MissingSkills = firstN(res.MissingSkills, 5)
		it.Blockers = res.Blockers
		it.Signals = firstN(res.Signals, 4)
		it.Job.Match = &listings.MatchBrief{Score: it.Score, Eligibility: it.Eligibility, Confidence: it.Confidence}
		return it, nil
	})
	if err != nil {
		return MatchPage{}, err
	}
	page := MatchPage{Data: items, Total: total}
	if page.Data == nil {
		page.Data = []MatchItem{}
	}
	if len(items) > limit {
		page.Data = items[:limit]
		if next := offset + limit; next <= listings.MaxOffset {
			c := listings.EncodeCursor(next)
			page.NextCursor = &c
		}
	}
	page.State, err = GetState(ctx, q, userID)
	return page, err
}

func firstN[T any](in []T, n int) []T {
	if len(in) > n {
		return in[:n]
	}
	if in == nil {
		return []T{}
	}
	return in
}

// AnalysisInfo describes what the platform knows about a job's requirements.
type AnalysisInfo struct {
	Method          string     `json:"method"` // best current analysis: LLM | DETERMINISTIC | NONE
	AnalyzerVersion string     `json:"analyzer_version"`
	AnalyzedAt      *time.Time `json:"analyzed_at"`
	Model           *string    `json:"model"`
	DeepStatus      string     `json:"deep_status"` // NONE | QUEUED | RUNNING | SUCCEEDED | FAILED
	DeepError       *string    `json:"deep_error"`
	Embedded        bool       `json:"embedded"`
}

func GetAnalysisInfo(ctx context.Context, q db.Querier, jobID uuid.UUID) (AnalysisInfo, error) {
	info := AnalysisInfo{Method: "NONE", DeepStatus: "NONE"}
	err := q.QueryRow(ctx, `SELECT a.method, a.analyzer_version, a.created_at, a.model
		FROM jobs j JOIN job_analyses a ON a.job_id = j.id AND a.content_hash = j.content_hash
		WHERE j.id = $1 ORDER BY (a.method = 'LLM') DESC LIMIT 1`, jobID).
		Scan(&info.Method, &info.AnalyzerVersion, &info.AnalyzedAt, &info.Model)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return info, err
	}
	err = q.QueryRow(ctx, `SELECT status, error FROM job_analysis_requests WHERE job_id = $1`, jobID).Scan(&info.DeepStatus, &info.DeepError)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return info, err
	}
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM job_embeddings e JOIN jobs j ON j.id = e.job_id
		WHERE e.job_id = $1 AND e.content_hash = j.content_hash)`, jobID).Scan(&info.Embedded); err != nil {
		return info, err
	}
	return info, nil
}

// Feedback is the user's reaction to a match.
type Feedback struct {
	Rating    string    `json:"rating"`
	Reason    *string   `json:"reason"`
	Note      string    `json:"note"`
	Dismissed bool      `json:"dismissed"`
	UpdatedAt time.Time `json:"updated_at"`
}

func GetFeedback(ctx context.Context, q db.Querier, userID, jobID uuid.UUID) (*Feedback, error) {
	var f Feedback
	err := q.QueryRow(ctx, `SELECT rating, reason, note, dismissed, updated_at FROM match_feedback WHERE user_id = $1 AND job_id = $2`,
		userID, jobID).Scan(&f.Rating, &f.Reason, &f.Note, &f.Dismissed, &f.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &f, err
}

// SkillGap is a missing skill and how many of the user's top matches ask for it.
type SkillGap struct {
	Skill string `json:"skill"`
	Jobs  int    `json:"jobs"`
}

// SkillGaps aggregates missing core skills across the user's 50 best
// non-ineligible matches.
func SkillGaps(ctx context.Context, q db.Querier, userID uuid.UUID) ([]SkillGap, int, error) {
	var considered int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM (SELECT 1 FROM job_match_results m JOIN jobs j ON j.id = m.job_id
		WHERE m.user_id = $1 AND m.algorithm_version = $2 AND m.eligibility <> 'INELIGIBLE' AND j.expired_at IS NULL LIMIT 50) t`,
		userID, AlgorithmVersion).Scan(&considered); err != nil {
		return nil, 0, err
	}
	rows, err := q.Query(ctx, `
		WITH top AS (
			SELECT m.result FROM job_match_results m JOIN jobs j ON j.id = m.job_id
			WHERE m.user_id = $1 AND m.algorithm_version = $2 AND m.eligibility <> 'INELIGIBLE' AND j.expired_at IS NULL
			ORDER BY m.overall_score DESC LIMIT 50
		)
		SELECT x, count(*)::int FROM top, jsonb_array_elements_text(top.result -> 'missing_skills') x
		GROUP BY x ORDER BY count(*) DESC, x LIMIT 12`, userID, AlgorithmVersion)
	if err != nil {
		return nil, 0, err
	}
	gaps, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (SkillGap, error) {
		var g SkillGap
		return g, r.Scan(&g.Skill, &g.Jobs)
	})
	if gaps == nil {
		gaps = []SkillGap{}
	}
	return gaps, considered, err
}
