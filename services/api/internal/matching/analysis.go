package matching

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/careerpilot/careerpilot/services/api/internal/aiclient"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
)

const (
	analyzeBatch    = 32
	maxPostingChars = 60_000
)

type pendingJob struct {
	ID          uuid.UUID
	Title       string
	Company     string
	Text        string
	ContentHash []byte
	NeedsEmbed  bool
}

// storedAnalysis is what job_analyses.analysis holds: the AI analysis plus a
// flat list of canonical slugs for candidate retrieval.
type storedAnalysis struct {
	aiclient.JobAnalysis
	SkillSlugs []string `json:"skill_slugs"`
}

func toStored(a aiclient.JobAnalysis) storedAnalysis {
	s := storedAnalysis{JobAnalysis: a, SkillSlugs: []string{}}
	for _, r := range a.Skills {
		if r.Slug != nil {
			s.SkillSlugs = append(s.SkillSlugs, *r.Slug)
		}
	}
	return s
}

// AnalyzePending analyses (and embeds) active jobs whose deterministic
// analysis or embedding is missing or stale, one batch per call. It returns
// how many jobs it processed and whether more remain.
func (s *Service) AnalyzePending(ctx context.Context) (int, bool, error) {
	if s.ai == nil {
		return 0, false, ErrAIUnavailable
	}
	info, err := s.ai.Info(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("ai info: %w", err)
	}
	version := info.AnalyzerVersions["jobs.deterministic"]
	if version == "" {
		return 0, false, fmt.Errorf("ai service does not report a deterministic analyzer version")
	}
	embedModel := ""
	if info.EmbeddingModel != nil && info.Has("embeddings") {
		embedModel = *info.EmbeddingModel
	}

	rows, err := s.pool.Query(ctx, `
		SELECT j.id, j.title, j.company_name, left(j.description_text, $3), j.content_hash,
		       ($2 <> '' AND (e.job_id IS NULL OR e.content_hash <> j.content_hash OR e.model <> $2))
		FROM jobs j
		LEFT JOIN job_analyses a ON a.job_id = j.id AND a.method = 'DETERMINISTIC'
		LEFT JOIN job_embeddings e ON e.job_id = j.id
		WHERE j.expired_at IS NULL AND j.duplicate_of IS NULL
		  AND (a.job_id IS NULL OR a.content_hash <> j.content_hash OR a.analyzer_version <> $1
		       OR ($2 <> '' AND (e.job_id IS NULL OR e.content_hash <> j.content_hash OR e.model <> $2)))
		ORDER BY j.first_seen_at DESC
		LIMIT $4`, version, embedModel, maxPostingChars, analyzeBatch+1)
	if err != nil {
		return 0, false, err
	}
	batch, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (pendingJob, error) {
		var p pendingJob
		err := r.Scan(&p.ID, &p.Title, &p.Company, &p.Text, &p.ContentHash, &p.NeedsEmbed)
		return p, err
	})
	if err != nil {
		return 0, false, err
	}
	more := len(batch) > analyzeBatch
	if more {
		batch = batch[:analyzeBatch]
	}
	if len(batch) == 0 {
		return 0, false, nil
	}

	// Embedding is the expensive step: only embed jobs whose vector is stale
	// (e.g. a catalogue bump re-analyses everything but re-embeds nothing).
	byID := make(map[string]pendingJob, len(batch))
	var embedReq, plainReq aiclient.AnalyzeJobsRequest
	embedReq.Mode, embedReq.Embed, plainReq.Mode = "deterministic", true, "deterministic"
	for _, p := range batch {
		in := aiclient.JobInput{ID: p.ID.String(), Title: p.Title, Company: p.Company, Text: p.Text}
		byID[in.ID] = p
		if p.NeedsEmbed {
			embedReq.Jobs = append(embedReq.Jobs, in)
		} else {
			plainReq.Jobs = append(plainReq.Jobs, in)
		}
	}
	var resp aiclient.AnalyzeJobsResponse
	for _, req := range []aiclient.AnalyzeJobsRequest{embedReq, plainReq} {
		if len(req.Jobs) == 0 {
			continue
		}
		part, err := s.ai.AnalyzeJobs(ctx, req)
		if err != nil {
			return 0, false, err
		}
		if part.EmbeddingModel != nil {
			resp.EmbeddingModel = part.EmbeddingModel
		}
		resp.Results = append(resp.Results, part.Results...)
	}
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		for _, res := range resp.Results {
			p, ok := byID[res.ID]
			if !ok {
				continue
			}
			if err := saveAnalysis(ctx, tx, p.ID, p.ContentHash, res.Analysis, nil); err != nil {
				return err
			}
			if len(res.Embedding) > 0 && resp.EmbeddingModel != nil {
				if _, err := tx.Exec(ctx, `INSERT INTO job_embeddings (job_id, model, content_hash, embedding)
					VALUES ($1, $2, $3, $4::vector)
					ON CONFLICT (job_id) DO UPDATE SET model = EXCLUDED.model, content_hash = EXCLUDED.content_hash,
						embedding = EXCLUDED.embedding, created_at = now()`,
					p.ID, *resp.EmbeddingModel, p.ContentHash, aiclient.VectorLiteral(res.Embedding)); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return 0, false, err
	}
	return len(batch), more, nil
}

func saveAnalysis(ctx context.Context, q db.Querier, jobID uuid.UUID, contentHash []byte, a aiclient.JobAnalysis, usage *aiclient.AnalyzeUsage) error {
	raw, err := json.Marshal(toStored(a))
	if err != nil {
		return err
	}
	var provider, model *string
	var in, out *int
	if usage != nil {
		provider, model, in, out = &usage.Provider, &usage.Model, &usage.InputTokens, &usage.OutputTokens
	}
	_, err = q.Exec(ctx, `INSERT INTO job_analyses (job_id, method, content_hash, analyzer_version, analysis, provider, model, input_tokens, output_tokens)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (job_id, method) DO UPDATE SET content_hash = EXCLUDED.content_hash, analyzer_version = EXCLUDED.analyzer_version,
			analysis = EXCLUDED.analysis, provider = EXCLUDED.provider, model = EXCLUDED.model,
			input_tokens = EXCLUDED.input_tokens, output_tokens = EXCLUDED.output_tokens, created_at = now()`,
		jobID, a.Method, contentHash, a.AnalyzerVersion, raw, provider, model, in, out)
	return err
}

// DeepAnalyze runs LLM extraction for one job (user-triggered). The result is
// shared: jobs are global, so later users reuse it while the content is unchanged.
func (s *Service) DeepAnalyze(ctx context.Context, jobID uuid.UUID) error {
	if s.ai == nil {
		return ErrAIUnavailable
	}
	var p pendingJob
	err := s.pool.QueryRow(ctx, `SELECT id, title, company_name, left(description_text, $2), content_hash FROM jobs WHERE id = $1`,
		jobID, maxPostingChars).Scan(&p.ID, &p.Title, &p.Company, &p.Text, &p.ContentHash)
	if err != nil {
		return err
	}
	var current bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM job_analyses WHERE job_id = $1 AND method = 'LLM' AND content_hash = $2)`,
		jobID, p.ContentHash).Scan(&current); err != nil {
		return err
	}
	if current {
		return nil
	}
	start := time.Now()
	resp, err := s.ai.AnalyzeJobs(ctx, aiclient.AnalyzeJobsRequest{Mode: "llm", Embed: false,
		Jobs: []aiclient.JobInput{{ID: p.ID.String(), Title: p.Title, Company: p.Company, Text: p.Text}}})
	if err != nil {
		return err
	}
	if len(resp.Results) != 1 {
		return fmt.Errorf("ai service returned %d results for one job", len(resp.Results))
	}
	if err := saveAnalysis(ctx, s.pool, p.ID, p.ContentHash, resp.Results[0].Analysis, resp.Usage); err != nil {
		return err
	}
	s.log.Info("deep job analysis stored", "job_id", jobID, "duration_ms", time.Since(start).Milliseconds())
	return nil
}
