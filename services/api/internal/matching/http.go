package matching

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/careerpilot/careerpilot/services/api/internal/aiclient"
	"github.com/careerpilot/careerpilot/services/api/internal/audit"
	"github.com/careerpilot/careerpilot/services/api/internal/auth"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ratelimit"
)

type HTTP struct {
	svc       *Service
	scheduler *Scheduler
	limiter   ratelimit.Limiter
}

func NewHTTP(svc *Service, scheduler *Scheduler, limiter ratelimit.Limiter) *HTTP {
	return &HTTP{svc: svc, scheduler: scheduler, limiter: limiter}
}

// Routes are mounted behind RequireAuth.
func (h *HTTP) Routes(r chi.Router) {
	r.Get("/matches", h.list)
	r.Get("/matches/status", h.status)
	r.Post("/matches/recompute", h.recompute)
	r.Get("/matches/skill-gaps", h.skillGaps)
	r.Get("/jobs/{id}/match", h.jobMatch)
	r.Put("/jobs/{id}/match/feedback", h.putFeedback)
	r.Delete("/jobs/{id}/match/feedback", h.deleteFeedback)
	r.Post("/jobs/{id}/analysis", h.requestDeepAnalysis)
}

func (h *HTTP) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := ListFilter{IncludeDismissed: q.Get("include_dismissed") == "true"}
	errs := map[string]string{}
	switch e := q.Get("eligibility"); e {
	case "", "ALL", Eligible, Unknown, Ineligible:
		f.Eligibility = e
	default:
		errs["eligibility"] = "Use ELIGIBLE, UNKNOWN, INELIGIBLE or ALL."
	}
	if s := q.Get("min_score"); s != "" {
		n, err := strconv.ParseFloat(s, 64)
		if err != nil || n < 0 || n > 100 {
			errs["min_score"] = "Between 0 and 100."
		}
		f.MinScore = n
	}
	limit := 20
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 50 {
			errs["limit"] = "Between 1 and 50."
		}
		limit = n
	}
	if len(errs) > 0 {
		httpx.WriteError(w, r, httpx.Validation(errs))
		return
	}
	page, err := ListMatches(r.Context(), h.svc.pool, auth.MustPrincipal(r.Context()).UserID, f, limit, q.Get("cursor"))
	if err != nil {
		if err.Error() == "bad cursor" {
			err = httpx.Validation(map[string]string{"cursor": "Invalid cursor."})
		}
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

func (h *HTTP) status(w http.ResponseWriter, r *http.Request) {
	st, err := GetState(r.Context(), h.svc.pool, auth.MustPrincipal(r.Context()).UserID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, st)
}

func (h *HTTP) recompute(w http.ResponseWriter, r *http.Request) {
	p := auth.MustPrincipal(r.Context())
	if ok, _, err := h.limiter.Allow(r.Context(), "matching:recompute:"+p.UserID.String(), 10, time.Hour); err == nil && !ok {
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeRateLimited, "Matches were recomputed recently. Please wait before trying again."))
		return
	}
	queued, err := h.scheduler.ScheduleUser(r.Context(), p.UserID, "MANUAL", 0)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	msg := "Match recomputation queued."
	if !queued {
		msg = "A match recomputation is already queued or running."
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{"queued": queued, "message": msg})
}

func (h *HTTP) skillGaps(w http.ResponseWriter, r *http.Request) {
	gaps, considered, err := SkillGaps(r.Context(), h.svc.pool, auth.MustPrincipal(r.Context()).UserID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": gaps, "matches_considered": considered})
}

type jobMatchResponse struct {
	Status     string       `json:"status"` // READY | PROFILE_INCOMPLETE | ANALYSIS_PENDING | AI_UNAVAILABLE
	Message    string       `json:"message,omitempty"`
	Match      *Result      `json:"match"`
	Analysis   AnalysisInfo `json:"analysis"`
	Feedback   *Feedback    `json:"feedback"`
	DeepCan    bool         `json:"deep_analysis_available"`
	ComputedAt *time.Time   `json:"computed_at"`
	Disclaimer string       `json:"disclaimer"`
}

const disclaimer = "A suitability score compares the posting with your profile. It is not a prediction of interviews or offers."

func (h *HTTP) jobMatch(w http.ResponseWriter, r *http.Request) {
	jobID, ok := pathID(w, r)
	if !ok {
		return
	}
	userID := auth.MustPrincipal(r.Context()).UserID
	resp := jobMatchResponse{Disclaimer: disclaimer}
	res, err := h.svc.EvaluateJob(r.Context(), userID, jobID)
	switch {
	case errors.Is(err, ErrJobNotFound):
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeNotFound, "Resource not found."))
		return
	case err == nil:
		resp.Status, resp.Match = "READY", &res
		now := time.Now().UTC()
		resp.ComputedAt = &now
	case errors.Is(err, ErrProfileIncomplete):
		resp.Status, resp.Message = "PROFILE_INCOMPLETE", "Add skills or work experience to your profile to see how this role fits."
	case errors.Is(err, ErrAnalysisPending):
		resp.Status, resp.Message = "ANALYSIS_PENDING", "This posting has not been analysed yet. Analysis runs shortly after discovery."
	case errors.Is(err, ErrAIUnavailable) || isAIError(err):
		resp.Status, resp.Message = "AI_UNAVAILABLE", "The analysis service is unavailable, so the match cannot be computed right now."
	default:
		httpx.WriteError(w, r, err)
		return
	}
	if resp.Analysis, err = GetAnalysisInfo(r.Context(), h.svc.pool, jobID); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if resp.Feedback, err = GetFeedback(r.Context(), h.svc.pool, userID, jobID); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	resp.DeepCan = h.deepAvailable(r.Context())
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func isAIError(err error) bool {
	var e *aiclient.Error
	return errors.As(err, &e)
}

func (h *HTTP) deepAvailable(ctx context.Context) bool {
	if h.svc.ai == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	info, err := h.svc.ai.Info(ctx)
	return err == nil && info.Has("jobs.analyze.llm")
}

var feedbackReasons = map[string]bool{"GOOD_FIT": true, "SKILLS_MISMATCH": true, "WRONG_SENIORITY": true, "LOCATION": true,
	"COMPENSATION": true, "NOT_INTERESTED": true, "SCORE_TOO_HIGH": true, "SCORE_TOO_LOW": true, "OTHER": true}

type feedbackInput struct {
	Rating    string  `json:"rating"`
	Reason    *string `json:"reason"`
	Note      string  `json:"note"`
	Dismissed bool    `json:"dismissed"`
}

// putFeedback records the user's reaction. It is stored for evaluating and
// improving the algorithm; it does not change the score.
func (h *HTTP) putFeedback(w http.ResponseWriter, r *http.Request) {
	jobID, ok := pathID(w, r)
	if !ok {
		return
	}
	var in feedbackInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	in.Note = strings.TrimSpace(in.Note)
	v := map[string]string{}
	if in.Rating != "UP" && in.Rating != "DOWN" {
		v["rating"] = "Use UP or DOWN."
	}
	if in.Reason != nil && !feedbackReasons[*in.Reason] {
		v["reason"] = "Unsupported reason."
	}
	if len([]rune(in.Note)) > 500 {
		v["note"] = "At most 500 characters."
	}
	if len(v) > 0 {
		httpx.WriteError(w, r, httpx.Validation(v))
		return
	}
	userID := auth.MustPrincipal(r.Context()).UserID
	err := db.InTx(r.Context(), h.svc.pool, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM jobs WHERE id = $1)`, jobID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return httpx.NewError(httpx.CodeNotFound, "Resource not found.")
		}
		var score *float64
		_ = tx.QueryRow(r.Context(), `SELECT overall_score::float8 FROM job_match_results WHERE user_id = $1 AND job_id = $2 AND algorithm_version = $3`,
			userID, jobID, AlgorithmVersion).Scan(&score)
		_, err := tx.Exec(r.Context(), `INSERT INTO match_feedback (id, user_id, job_id, rating, reason, note, dismissed, algorithm_version, score_at_feedback)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (user_id, job_id) DO UPDATE SET rating = EXCLUDED.rating, reason = EXCLUDED.reason, note = EXCLUDED.note,
				dismissed = EXCLUDED.dismissed, algorithm_version = EXCLUDED.algorithm_version,
				score_at_feedback = EXCLUDED.score_at_feedback, updated_at = now()`,
			ids.New(), userID, jobID, in.Rating, in.Reason, in.Note, in.Dismissed, AlgorithmVersion, score)
		return err
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	fb, err := GetFeedback(r.Context(), h.svc.pool, userID, jobID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, fb)
}

func (h *HTTP) deleteFeedback(w http.ResponseWriter, r *http.Request) {
	jobID, ok := pathID(w, r)
	if !ok {
		return
	}
	_, err := h.svc.pool.Exec(r.Context(), `DELETE FROM match_feedback WHERE user_id = $1 AND job_id = $2`, auth.MustPrincipal(r.Context()).UserID, jobID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// requestDeepAnalysis queues an LLM analysis of one posting. It costs
// provider tokens, so it is rate limited per user and audited.
func (h *HTTP) requestDeepAnalysis(w http.ResponseWriter, r *http.Request) {
	jobID, ok := pathID(w, r)
	if !ok {
		return
	}
	p := auth.MustPrincipal(r.Context())
	if !h.deepAvailable(r.Context()) {
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeAIUnavailable, "Deep analysis needs an AI provider, and none is configured."))
		return
	}
	var contentHash []byte
	if err := h.svc.pool.QueryRow(r.Context(), `SELECT content_hash FROM jobs WHERE id = $1`, jobID).Scan(&contentHash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = httpx.NewError(httpx.CodeNotFound, "Resource not found.")
		}
		httpx.WriteError(w, r, err)
		return
	}
	var current bool
	_ = h.svc.pool.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM job_analyses WHERE job_id = $1 AND method = 'LLM' AND content_hash = $2)`,
		jobID, contentHash).Scan(&current)
	if current {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"queued": false, "message": "This posting already has a current deep analysis."})
		return
	}
	if ok, _, err := h.limiter.Allow(r.Context(), "matching:deep:"+p.UserID.String(), 20, 24*time.Hour); err == nil && !ok {
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeRateLimited, "You have reached today's limit of 20 deep analyses."))
		return
	}
	var queued bool
	err := db.InTx(r.Context(), h.svc.pool, func(tx pgx.Tx) error {
		res, err := h.scheduler.Queue.InsertTx(r.Context(), tx, DeepAnalyzeArgs{JobID: jobID, UserID: p.UserID}, nil)
		if err != nil {
			return err
		}
		queued = !res.UniqueSkippedAsDuplicate
		if queued {
			if _, err := tx.Exec(r.Context(), `INSERT INTO job_analysis_requests (job_id, status, requested_by) VALUES ($1, 'QUEUED', $2)
				ON CONFLICT (job_id) DO UPDATE SET status = 'QUEUED', error = NULL, requested_by = $2, updated_at = now()`, jobID, p.UserID); err != nil {
				return err
			}
		}
		return audit.Record(r.Context(), tx, audit.Event{UserID: &p.UserID, Action: "job.analysis.deep_requested", SubjectType: "job",
			SubjectID: &jobID, Outcome: audit.Success, Metadata: map[string]any{"deduplicated": !queued}})
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	msg := "Deep analysis queued. The match updates when it completes."
	if !queued {
		msg = "A deep analysis of this posting is already in progress."
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{"queued": queued, "message": msg})
}

func pathID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := ids.Parse(chi.URLParam(r, "id"))
	if !ok {
		httpx.WriteError(w, r, httpx.NewError(httpx.CodeNotFound, "Resource not found."))
	}
	return id, ok
}
