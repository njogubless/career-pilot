package matching

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/careerpilot/careerpilot/services/api/internal/aiclient"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
	"github.com/careerpilot/careerpilot/services/api/internal/profiles"
)

var (
	ErrAIUnavailable     = errors.New("matching: AI service is not configured")
	ErrProfileIncomplete = errors.New("matching: profile has no skills or experience")
	ErrAnalysisPending   = errors.New("matching: job has not been analysed yet")
	ErrJobNotFound       = errors.New("matching: job not found")
)

// candidateLimit bounds each retrieval channel (semantic and skill overlap).
const candidateLimit = 400

type Service struct {
	pool     *pgxpool.Pool
	ai       *aiclient.Client
	profiles *profiles.Service
	log      *slog.Logger
	weights  Weights
	now      func() time.Time
}

func NewService(pool *pgxpool.Pool, ai *aiclient.Client, prof *profiles.Service, log *slog.Logger) *Service {
	return &Service{pool: pool, ai: ai, profiles: prof, log: log, weights: DefaultWeights, now: time.Now}
}

type prepared struct {
	Profile      Profile
	Hash         string
	HasEmbedding bool
	Ready        bool // enough profile data to score
}

// prepare loads the profile, refreshes canonical skill links and text-skill
// mentions, and makes sure the profile embedding matches its current text.
func (s *Service) prepare(ctx context.Context, userID uuid.UUID) (prepared, error) {
	if s.ai == nil {
		return prepared{}, ErrAIUnavailable
	}
	pr, err := s.profiles.Get(ctx, userID)
	if err != nil {
		return prepared{}, fmt.Errorf("load profile: %w", err)
	}
	now := s.now().UTC()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC) // stable within a month
	in := buildInputs(pr, nil, month)

	var skillIDs []uuid.UUID
	var terms []string
	for _, sk := range pr.Skills {
		if usable(sk.Provenance) {
			skillIDs = append(skillIDs, sk.ID)
			terms = append(terms, sk.Name)
		}
	}
	norm, err := s.ai.NormalizeSkills(ctx, aiclient.NormalizeRequest{Terms: terms, Texts: in.Texts})
	if err != nil {
		return prepared{}, fmt.Errorf("normalize skills: %w", err)
	}
	if len(norm.Terms) != len(terms) || len(norm.Texts) != len(in.Texts) {
		return prepared{}, fmt.Errorf("normalize skills: mismatched response lengths")
	}
	slugs := map[string]string{}
	for i, t := range norm.Terms {
		var slug *string
		if t.Skill != nil {
			slugs[skillIDs[i].String()] = t.Skill.Slug
			slug = &t.Skill.Slug
		}
		if _, err := s.pool.Exec(ctx, `UPDATE profile_skills SET skill_slug = $3, skill_catalogue_version = $4
			WHERE id = $1 AND user_id = $2 AND (skill_slug IS DISTINCT FROM $3 OR skill_catalogue_version IS DISTINCT FROM $4)`,
			skillIDs[i], userID, slug, norm.CatalogueVersion); err != nil {
			return prepared{}, err
		}
	}
	in = buildInputs(pr, slugs, month)
	for i, mentions := range norm.Texts {
		for _, m := range mentions {
			in.Profile.TextSkills = append(in.Profile.TextSkills, TextSkill{Slug: m.Slug, Name: m.Name, Evidence: m.Evidence,
				Origin: in.TextOrigins[i].Label, Verified: in.TextOrigins[i].Verified})
		}
	}
	slices.SortStableFunc(in.Profile.TextSkills, func(a, b TextSkill) int {
		if c := strings.Compare(a.Slug, b.Slug); c != 0 {
			return c
		}
		return strings.Compare(a.Origin, b.Origin)
	})

	embHash := hashOf(in.EmbeddingText)
	var storedHash, storedModel *string
	var hasVec bool
	err = s.pool.QueryRow(ctx, `SELECT embedding_input_hash, embedding_model, embedding IS NOT NULL FROM match_profiles WHERE user_id = $1`, userID).
		Scan(&storedHash, &storedModel, &hasVec)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return prepared{}, err
	}
	model := ""
	var vec *string
	keep := false
	switch {
	case in.EmbeddingText == "":
	case hasVec && storedHash != nil && *storedHash == embHash && storedModel != nil:
		model, keep = *storedModel, true
	default:
		emb, err := s.ai.Embed(ctx, []string{in.EmbeddingText})
		switch {
		case aiclient.IsCode(err, "EMBEDDINGS_UNAVAILABLE"):
			s.log.Warn("embeddings unavailable; matching without semantic similarity")
		case err != nil:
			return prepared{}, fmt.Errorf("embed profile: %w", err)
		case len(emb.Vectors) == 1:
			model = emb.Model
			lit := aiclient.VectorLiteral(emb.Vectors[0])
			vec = &lit
		}
	}
	textSkills, _ := json.Marshal(in.Profile.TextSkills)
	var modelArg *string
	if model != "" {
		modelArg = &model
	}
	if keep {
		_, err = s.pool.Exec(ctx, `INSERT INTO match_profiles (user_id, text_skills_hash, text_skills, catalogue_version)
			VALUES ($1, $2, $3, $4) ON CONFLICT (user_id) DO UPDATE SET text_skills_hash = EXCLUDED.text_skills_hash,
			text_skills = EXCLUDED.text_skills, catalogue_version = EXCLUDED.catalogue_version, updated_at = now()`,
			userID, hashOf(terms, in.Texts), textSkills, norm.CatalogueVersion)
	} else {
		_, err = s.pool.Exec(ctx, `INSERT INTO match_profiles (user_id, text_skills_hash, text_skills, catalogue_version, embedding_input_hash, embedding_model, embedding)
			VALUES ($1, $2, $3, $4, $5, $6, $7::vector) ON CONFLICT (user_id) DO UPDATE SET text_skills_hash = EXCLUDED.text_skills_hash,
			text_skills = EXCLUDED.text_skills, catalogue_version = EXCLUDED.catalogue_version, embedding_input_hash = EXCLUDED.embedding_input_hash,
			embedding_model = EXCLUDED.embedding_model, embedding = EXCLUDED.embedding, updated_at = now()`,
			userID, hashOf(terms, in.Texts), textSkills, norm.CatalogueVersion, embHash, modelArg, vec)
	}
	if err != nil {
		return prepared{}, err
	}
	in.Profile.EmbeddingModel = model
	usableExp := false
	for _, e := range pr.Experiences {
		usableExp = usableExp || usable(e.Provenance)
	}
	return prepared{
		Profile:      in.Profile,
		Hash:         hashOf(in.Profile, embHash),
		HasEmbedding: model != "",
		Ready:        len(in.Profile.Skills) > 0 || usableExp,
	}, nil
}

// Stats summarises one matching run.
type Stats struct {
	Candidates int
	Scored     int
	Duration   time.Duration
}

// ComputeForUser retrieves candidate jobs and stores a fresh, versioned match
// result for each. Results for jobs no longer retrieved are removed unless the
// user left feedback on them.
func (s *Service) ComputeForUser(ctx context.Context, userID uuid.UUID) (Stats, error) {
	start := s.now()
	if _, err := s.pool.Exec(ctx, `INSERT INTO match_states (user_id, status, started_at) VALUES ($1, 'RUNNING', now())
		ON CONFLICT (user_id) DO UPDATE SET status = 'RUNNING', started_at = now(), error = NULL, updated_at = now()`, userID); err != nil {
		return Stats{}, err
	}
	stats, hash, err := s.compute(ctx, userID)
	if err != nil {
		msg := "Matching failed; it will be retried."
		if errors.Is(err, ErrProfileIncomplete) {
			msg = "Add skills or experience to your profile to see matches."
		}
		_, _ = s.pool.Exec(context.WithoutCancel(ctx), `UPDATE match_states SET status = 'FAILED', error = $2, updated_at = now() WHERE user_id = $1`, userID, msg)
		return stats, err
	}
	stats.Duration = s.now().Sub(start)
	_, err = s.pool.Exec(ctx, `UPDATE match_states SET status = 'READY', algorithm_version = $2, candidates = $3, scored = $4,
		profile_hash = $5, completed_at = now(), updated_at = now() WHERE user_id = $1`,
		userID, AlgorithmVersion, stats.Candidates, stats.Scored, hash)
	return stats, err
}

func (s *Service) compute(ctx context.Context, userID uuid.UUID) (Stats, string, error) {
	prep, err := s.prepare(ctx, userID)
	if err != nil {
		return Stats{}, "", err
	}
	if !prep.Ready {
		return Stats{}, prep.Hash, ErrProfileIncomplete
	}
	var stats Stats
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		ids, err := candidateIDs(ctx, tx, userID, prep)
		if err != nil {
			return err
		}
		stats.Candidates = len(ids)
		jobs, err := loadJobs(ctx, tx, userID, ids)
		if err != nil {
			return err
		}
		results := make([]Result, len(jobs))
		for i, j := range jobs {
			results[i] = Evaluate(prep.Profile, j, s.weights)
		}
		if err := saveResults(ctx, tx, userID, prep.Hash, jobs, results); err != nil {
			return err
		}
		stats.Scored = len(results)
		scored := make([]uuid.UUID, len(jobs))
		for i, j := range jobs {
			scored[i] = j.ID
		}
		_, err = tx.Exec(ctx, `DELETE FROM job_match_results r WHERE r.user_id = $1 AND r.algorithm_version = $2
			AND NOT (r.job_id = ANY($3)) AND NOT EXISTS (SELECT 1 FROM match_feedback f WHERE f.user_id = r.user_id AND f.job_id = r.job_id)`,
			userID, AlgorithmVersion, scored)
		return err
	})
	return stats, prep.Hash, err
}

// candidateIDs unions semantic nearest neighbours, skill-overlap matches and
// jobs the user gave feedback on. Only active, canonical jobs qualify.
func candidateIDs(ctx context.Context, tx pgx.Tx, userID uuid.UUID, prep prepared) ([]uuid.UUID, error) {
	slugs := map[string]bool{}
	for _, sk := range prep.Profile.Skills {
		if sk.Slug != "" {
			slugs[sk.Slug] = true
		}
	}
	for _, t := range prep.Profile.TextSkills {
		slugs[t.Slug] = true
	}
	slugList := make([]string, 0, len(slugs))
	for k := range slugs {
		slugList = append(slugList, k)
	}
	slices.Sort(slugList)
	// HNSW returns at most ef_search rows per scan; widen it for this query.
	if _, err := tx.Exec(ctx, "SET LOCAL hnsw.ef_search = 500"); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		WITH vec AS (
			SELECT je.job_id FROM match_profiles mp
			JOIN job_embeddings je ON je.model = mp.embedding_model
			JOIN jobs j ON j.id = je.job_id AND je.content_hash = j.content_hash
			WHERE mp.user_id = $1 AND mp.embedding IS NOT NULL AND j.expired_at IS NULL AND j.duplicate_of IS NULL
			ORDER BY je.embedding <=> mp.embedding
			LIMIT $2
		), sk AS (
			SELECT a.job_id FROM job_analyses a
			JOIN jobs j ON j.id = a.job_id AND a.content_hash = j.content_hash
			WHERE cardinality($3::text[]) > 0 AND a.method = 'DETERMINISTIC' AND (a.analysis -> 'skill_slugs') ?| $3
			  AND j.expired_at IS NULL AND j.duplicate_of IS NULL
			ORDER BY (SELECT count(*) FROM jsonb_array_elements_text(a.analysis -> 'skill_slugs') x WHERE x = ANY($3)) DESC,
			         COALESCE(j.posted_at, j.first_seen_at) DESC, j.id
			LIMIT $2
		), fb AS (
			SELECT f.job_id FROM match_feedback f JOIN jobs j ON j.id = f.job_id WHERE f.user_id = $1 AND j.expired_at IS NULL
		)
		SELECT job_id FROM vec UNION SELECT job_id FROM sk UNION SELECT job_id FROM fb`,
		userID, candidateLimit, slugList)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// loadJobs builds job snapshots with their best current analysis (LLM over
// deterministic, same content hash only) and profile similarity.
func loadJobs(ctx context.Context, q db.Querier, userID uuid.UUID, ids []uuid.UUID) ([]Job, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := q.Query(ctx, `
		SELECT j.id, j.title, j.company_name, j.location_raw, j.location_countries, j.remote_status, j.remote_scope,
		       j.remote_countries, j.remote_region, j.employment_type, j.salary_annual_min::float8, j.salary_annual_max::float8,
		       COALESCE(j.salary_currency, ''), j.content_hash, a.analysis, COALESCE(je.model, ''),
		       CASE WHEN mp.embedding IS NOT NULL AND je.model = mp.embedding_model THEN 1 - (je.embedding <=> mp.embedding) END
		FROM jobs j
		JOIN LATERAL (
			SELECT analysis FROM job_analyses WHERE job_id = j.id AND content_hash = j.content_hash
			ORDER BY (method = 'LLM') DESC LIMIT 1
		) a ON true
		LEFT JOIN job_embeddings je ON je.job_id = j.id AND je.content_hash = j.content_hash
		LEFT JOIN match_profiles mp ON mp.user_id = $1
		WHERE j.id = ANY($2)
		ORDER BY j.id`, userID, ids)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Job, error) {
		var j Job
		var raw []byte
		if err := r.Scan(&j.ID, &j.Title, &j.Company, &j.LocationRaw, &j.LocationCountries, &j.RemoteStatus, &j.RemoteScope,
			&j.RemoteCountries, &j.RemoteRegion, &j.EmploymentType, &j.SalaryAnnualMin, &j.SalaryAnnualMax,
			&j.SalaryCurrency, &j.ContentHash, &raw, &j.EmbeddingModel, &j.Similarity); err != nil {
			return j, err
		}
		if err := json.Unmarshal(raw, &j.Analysis); err != nil {
			return j, fmt.Errorf("decode analysis for job %s: %w", j.ID, err)
		}
		if j.Similarity != nil {
			v := round3(*j.Similarity)
			j.Similarity = &v // rounding keeps the score stable across float noise
		}
		return j, nil
	})
}

func round3(v float64) float64 { return float64(int64(v*1000+0.5)) / 1000 }

func jobHash(j Job) string {
	return hashOf(j.ContentHash, j.Analysis.Method, j.Analysis.AnalyzerVersion, j.EmbeddingModel, j.Similarity,
		j.Title, j.LocationCountries, j.RemoteStatus, j.RemoteScope, j.RemoteCountries, j.RemoteRegion, j.EmploymentType,
		j.SalaryAnnualMin, j.SalaryAnnualMax, j.SalaryCurrency)
}

func saveResults(ctx context.Context, tx pgx.Tx, userID uuid.UUID, profileHash string, jobs []Job, results []Result) error {
	batch := &pgx.Batch{}
	for i, r := range results {
		raw, err := json.Marshal(r)
		if err != nil {
			return err
		}
		weights, _ := json.Marshal(r.Weights)
		batch.Queue(`INSERT INTO job_match_results (id, user_id, job_id, algorithm_version, overall_score, eligibility, confidence,
				analysis_method, result, weights, profile_hash, job_hash)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			ON CONFLICT (user_id, job_id, algorithm_version) DO UPDATE SET overall_score = EXCLUDED.overall_score,
				eligibility = EXCLUDED.eligibility, confidence = EXCLUDED.confidence, analysis_method = EXCLUDED.analysis_method,
				result = EXCLUDED.result, weights = EXCLUDED.weights, profile_hash = EXCLUDED.profile_hash,
				job_hash = EXCLUDED.job_hash, computed_at = now()`,
			ids.New(), userID, jobs[i].ID, r.AlgorithmVersion, r.Score, r.Eligibility, r.Confidence,
			r.AnalysisMethod, raw, weights, profileHash, jobHash(jobs[i]))
	}
	return tx.SendBatch(ctx, batch).Close()
}

// EvaluateJob computes (and stores) the match for one job on demand.
func (s *Service) EvaluateJob(ctx context.Context, userID, jobID uuid.UUID) (Result, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM jobs WHERE id = $1)`, jobID).Scan(&exists); err != nil {
		return Result{}, err
	}
	if !exists {
		return Result{}, ErrJobNotFound
	}
	prep, err := s.prepare(ctx, userID)
	if err != nil {
		return Result{}, err
	}
	if !prep.Ready {
		return Result{}, ErrProfileIncomplete
	}
	jobs, err := loadJobs(ctx, s.pool, userID, []uuid.UUID{jobID})
	if err != nil {
		return Result{}, err
	}
	if len(jobs) == 0 {
		return Result{}, ErrAnalysisPending
	}
	r := Evaluate(prep.Profile, jobs[0], s.weights)
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		return saveResults(ctx, tx, userID, prep.Hash, jobs, []Result{r})
	})
	return r, err
}

// UsersToMatch lists users with enough profile data to be matched.
func (s *Service) UsersToMatch(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `SELECT u.id FROM users u WHERE
		EXISTS (SELECT 1 FROM profile_skills s WHERE s.user_id = u.id AND s.verification <> 'REJECTED')
		OR EXISTS (SELECT 1 FROM work_experiences e WHERE e.user_id = u.id AND e.verification <> 'REJECTED')
		ORDER BY u.id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// ProfileChanged reports whether the user's matching inputs differ from the
// last completed run (used to rerun after edits made during a run).
func (s *Service) ProfileChanged(ctx context.Context, userID uuid.UUID) (bool, error) {
	var last *string
	err := s.pool.QueryRow(ctx, `SELECT profile_hash FROM match_states WHERE user_id = $1`, userID).Scan(&last)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	prep, err := s.prepare(ctx, userID)
	if err != nil {
		return false, err
	}
	return last == nil || *last != prep.Hash, nil
}
