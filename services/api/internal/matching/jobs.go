package matching

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/careerpilot/careerpilot/services/api/internal/aiclient"
	"github.com/careerpilot/careerpilot/services/api/internal/auth"
	"github.com/careerpilot/careerpilot/services/api/internal/jobs"
)

// Uniqueness while a job is waiting or running (completed jobs do not block
// new inserts). Running is required by River; edits made during a run are
// caught by the ProfileChanged re-check.
var activeStates = []rivertype.JobState{
	rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRunning,
	rivertype.JobStateRetryable, rivertype.JobStateScheduled,
}

// ---- analyze_jobs: deterministic analysis + embeddings for new/changed jobs ----

type AnalyzeJobsArgs struct {
	Trigger string `json:"trigger"`
}

func (AnalyzeJobsArgs) Kind() string { return "analyze_jobs" }

func (AnalyzeJobsArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 6, UniqueOpts: river.UniqueOpts{ByState: activeStates}}
}

type AnalyzeJobsWorker struct {
	river.WorkerDefaults[AnalyzeJobsArgs]
	Service *Service
	Log     *slog.Logger
}

const analyzeBudget = 8 * time.Minute

func (w *AnalyzeJobsWorker) Timeout(*river.Job[AnalyzeJobsArgs]) time.Duration {
	return analyzeBudget + 2*time.Minute
}

func (w *AnalyzeJobsWorker) Work(ctx context.Context, job *river.Job[AnalyzeJobsArgs]) error {
	deadline := time.Now().Add(analyzeBudget)
	total := 0
	for {
		n, more, err := w.Service.AnalyzePending(ctx)
		if errors.Is(err, ErrAIUnavailable) {
			return river.JobCancel(err)
		}
		if err != nil {
			return err
		}
		total += n
		if !more {
			break
		}
		if time.Now().After(deadline) {
			w.Log.Info("job analysis continuing", "analyzed", total)
			return river.JobSnooze(time.Second) // resume in a fresh run
		}
	}
	w.Log.Info("job analysis complete", "analyzed", total)
	if total == 0 {
		return nil
	}
	client, err := river.ClientFromContextSafely[pgx.Tx](ctx)
	if err != nil {
		return err
	}
	_, err = client.Insert(ctx, MatchAllArgs{Trigger: "JOBS_ANALYZED"}, nil)
	return err
}

// ---- match_all: fan out one match_user per eligible user ----

type MatchAllArgs struct {
	Trigger string `json:"trigger"`
}

func (MatchAllArgs) Kind() string { return "match_all" }

func (MatchAllArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByState: activeStates}}
}

type MatchAllWorker struct {
	river.WorkerDefaults[MatchAllArgs]
	Service *Service
}

func (w *MatchAllWorker) Work(ctx context.Context, job *river.Job[MatchAllArgs]) error {
	users, err := w.Service.UsersToMatch(ctx)
	if err != nil || len(users) == 0 {
		return err
	}
	client, err := river.ClientFromContextSafely[pgx.Tx](ctx)
	if err != nil {
		return err
	}
	params := make([]river.InsertManyParams, len(users))
	for i, u := range users {
		params[i] = river.InsertManyParams{Args: MatchUserArgs{UserID: u, Trigger: job.Args.Trigger}}
	}
	_, err = client.InsertMany(ctx, params)
	return err
}

// ---- match_user ----

type MatchUserArgs struct {
	UserID  uuid.UUID `json:"user_id" river:"unique"`
	Trigger string    `json:"trigger"`
}

func (MatchUserArgs) Kind() string { return "match_user" }

func (MatchUserArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 4, UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: activeStates}}
}

type MatchUserWorker struct {
	river.WorkerDefaults[MatchUserArgs]
	Service *Service
	Log     *slog.Logger
}

func (w *MatchUserWorker) Timeout(*river.Job[MatchUserArgs]) time.Duration { return 5 * time.Minute }

func (w *MatchUserWorker) Work(ctx context.Context, job *river.Job[MatchUserArgs]) error {
	stats, err := w.Service.ComputeForUser(ctx, job.Args.UserID)
	if errors.Is(err, ErrProfileIncomplete) || errors.Is(err, ErrAIUnavailable) || isPermanentAI(err) {
		return river.JobCancel(err)
	}
	if err != nil {
		return err
	}
	w.Log.Info("matches computed", "user_id", job.Args.UserID, "candidates", stats.Candidates, "scored", stats.Scored,
		"duration_ms", stats.Duration.Milliseconds(), "trigger", job.Args.Trigger)
	if changed, err := w.Service.ProfileChanged(ctx, job.Args.UserID); err == nil && changed {
		return river.JobSnooze(5 * time.Second) // profile edited during the run: compute again
	}
	return nil
}

func isPermanentAI(err error) bool {
	var e *aiclient.Error
	return errors.As(err, &e) && !e.Retryable
}

// ---- deep_analyze: user-requested LLM analysis of one job ----

type DeepAnalyzeArgs struct {
	JobID  uuid.UUID `json:"job_id" river:"unique"`
	UserID uuid.UUID `json:"user_id"`
}

func (DeepAnalyzeArgs) Kind() string { return "deep_analyze" }

func (DeepAnalyzeArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: activeStates}}
}

type DeepAnalyzeWorker struct {
	river.WorkerDefaults[DeepAnalyzeArgs]
	Service *Service
}

func (w *DeepAnalyzeWorker) Timeout(*river.Job[DeepAnalyzeArgs]) time.Duration {
	return 5 * time.Minute
}

func (w *DeepAnalyzeWorker) Work(ctx context.Context, job *river.Job[DeepAnalyzeArgs]) error {
	s := w.Service
	_, _ = s.pool.Exec(ctx, `UPDATE job_analysis_requests SET status = 'RUNNING', updated_at = now() WHERE job_id = $1`, job.Args.JobID)
	err := s.DeepAnalyze(ctx, job.Args.JobID)
	if err == nil {
		_, err = s.pool.Exec(ctx, `UPDATE job_analysis_requests SET status = 'SUCCEEDED', error = NULL, updated_at = now() WHERE job_id = $1`, job.Args.JobID)
		if err == nil {
			client, cerr := river.ClientFromContextSafely[pgx.Tx](ctx)
			if cerr == nil {
				_, _ = client.Insert(ctx, MatchUserArgs{UserID: job.Args.UserID, Trigger: "DEEP_ANALYSIS"}, nil)
			}
		}
		return err
	}
	permanent := errors.Is(err, ErrAIUnavailable) || errors.Is(err, pgx.ErrNoRows) || isPermanentAI(err)
	if permanent || job.Attempt >= job.MaxAttempts {
		msg := "The analysis could not be completed."
		if aiclient.IsCode(err, "AI_PROVIDER_UNAVAILABLE") || errors.Is(err, ErrAIUnavailable) {
			msg = "No AI provider is configured for deep analysis."
		} else if aiclient.IsCode(err, "AI_REFUSED") {
			msg = "The AI provider declined to analyse this posting."
		}
		_, _ = s.pool.Exec(context.WithoutCancel(ctx), `UPDATE job_analysis_requests SET status = 'FAILED', error = $2, updated_at = now() WHERE job_id = $1`,
			job.Args.JobID, msg)
	}
	if permanent {
		return river.JobCancel(err)
	}
	return err
}

// ---- Scheduling ----

// PeriodicJobs keeps analyses current and refreshes matches regularly.
func PeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(30*time.Minute),
			func() (river.JobArgs, *river.InsertOpts) { return AnalyzeJobsArgs{Trigger: "SCHEDULED"}, nil },
			&river.PeriodicJobOpts{RunOnStart: true}),
		river.NewPeriodicJob(river.PeriodicInterval(12*time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return MatchAllArgs{Trigger: "SCHEDULED"}, nil }, nil),
	}
}

// Scheduler enqueues matching work from the API process.
type Scheduler struct {
	Pool  *pgxpool.Pool
	Queue *jobs.Client
	Log   *slog.Logger
}

// ScheduleUser queues a (debounced) match recomputation for one user.
func (s *Scheduler) ScheduleUser(ctx context.Context, userID uuid.UUID, trigger string, delay time.Duration) (bool, error) {
	res, err := s.Queue.Insert(ctx, MatchUserArgs{UserID: userID, Trigger: trigger}, &river.InsertOpts{ScheduledAt: time.Now().Add(delay)})
	if err != nil {
		return false, err
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO match_states (user_id, status) VALUES ($1, 'QUEUED')
		ON CONFLICT (user_id) DO UPDATE SET status = 'QUEUED', updated_at = now() WHERE match_states.status <> 'RUNNING'`, userID)
	return !res.UniqueSkippedAsDuplicate, err
}

// ScheduleAnalysis queues analysis of new or changed jobs (after discovery).
func (s *Scheduler) ScheduleAnalysis(ctx context.Context, trigger string) error {
	_, err := s.Queue.Insert(ctx, AnalyzeJobsArgs{Trigger: trigger}, nil)
	return err
}

// RecomputeOnWrite schedules a match recomputation after successful writes
// to the user's profile or preferences, debounced by 20 seconds so a burst of
// edits produces one run.
func (s *Scheduler) RecomputeOnWrite(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || !profilePath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		if sw.status >= 300 {
			return
		}
		p, ok := auth.PrincipalFrom(r.Context())
		if !ok {
			return
		}
		if _, err := s.ScheduleUser(context.WithoutCancel(r.Context()), p.UserID, "PROFILE_CHANGED", 20*time.Second); err != nil {
			s.Log.Warn("could not schedule match recomputation", "err", err)
		}
	})
}

func profilePath(path string) bool {
	return strings.HasPrefix(path, "/api/v1/profiles/me") ||
		(strings.HasPrefix(path, "/api/v1/profile-extractions/") && strings.HasSuffix(path, "/apply"))
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wrote {
		w.status, w.wrote = code, true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(b)
}
