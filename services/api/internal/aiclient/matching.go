package aiclient

import (
	"context"
	"strconv"
	"strings"
)

// JobInput is one posting sent for analysis.
type JobInput struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Company string `json:"company"`
	Text    string `json:"text"`
}

type AnalyzeJobsRequest struct {
	Mode  string     `json:"mode"` // "deterministic" | "llm"
	Embed bool       `json:"embed"`
	Jobs  []JobInput `json:"jobs"`
}

// SkillRequirement mirrors the AI service schema (services/ai/app/jobs/schemas.py).
type SkillRequirement struct {
	Slug       *string `json:"slug"`
	Name       string  `json:"name"`
	Importance string  `json:"importance"` // REQUIRED | PREFERRED | MENTIONED
	Evidence   string  `json:"evidence"`
}

type JobAnalysis struct {
	Method                    string             `json:"method"`
	AnalyzerVersion           string             `json:"analyzer_version"`
	Skills                    []SkillRequirement `json:"skills"`
	MinYears                  *int               `json:"min_years"`
	YearsEvidence             *string            `json:"years_evidence"`
	Seniority                 string             `json:"seniority"`
	SeniorityEvidence         *string            `json:"seniority_evidence"`
	Sponsorship               string             `json:"sponsorship"`
	SponsorshipEvidence       *string            `json:"sponsorship_evidence"`
	WorkAuthorization         []string           `json:"work_authorization"`
	WorkAuthorizationEvidence *string            `json:"work_authorization_evidence"`
	Degree                    string             `json:"degree"`
	DegreeRequired            *bool              `json:"degree_required"`
	DegreeEvidence            *string            `json:"degree_evidence"`
	Warnings                  []string           `json:"warnings"`
}

type AnalyzeResult struct {
	ID        string      `json:"id"`
	Analysis  JobAnalysis `json:"analysis"`
	Embedding []float64   `json:"embedding"`
}

type AnalyzeUsage struct {
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	PromptVersion string `json:"prompt_version"`
	InputTokens   int    `json:"input_tokens"`
	OutputTokens  int    `json:"output_tokens"`
	LatencyMS     int    `json:"latency_ms"`
}

type AnalyzeJobsResponse struct {
	Results        []AnalyzeResult `json:"results"`
	EmbeddingModel *string         `json:"embedding_model"`
	Usage          *AnalyzeUsage   `json:"usage"`
}

func (c *Client) AnalyzeJobs(ctx context.Context, in AnalyzeJobsRequest) (AnalyzeJobsResponse, error) {
	var out AnalyzeJobsResponse
	err := c.postJSON(ctx, "/internal/v1/jobs/analyze", in, &out)
	return out, err
}

type CanonicalSkill struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Category string `json:"category"`
}

type TermResult struct {
	Term  string          `json:"term"`
	Skill *CanonicalSkill `json:"skill"`
}

type TextMention struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Evidence string `json:"evidence"`
}

type NormalizeRequest struct {
	Terms []string `json:"terms"`
	Texts []string `json:"texts"`
}

type NormalizeResponse struct {
	CatalogueVersion string          `json:"catalogue_version"`
	Terms            []TermResult    `json:"terms"`
	Texts            [][]TextMention `json:"texts"`
}

func (c *Client) NormalizeSkills(ctx context.Context, in NormalizeRequest) (NormalizeResponse, error) {
	if in.Terms == nil {
		in.Terms = []string{}
	}
	if in.Texts == nil {
		in.Texts = []string{}
	}
	var out NormalizeResponse
	err := c.postJSON(ctx, "/internal/v1/skills/normalize", in, &out)
	return out, err
}

type EmbedResponse struct {
	Model   string      `json:"model"`
	Dim     int         `json:"dim"`
	Vectors [][]float64 `json:"vectors"`
}

func (c *Client) Embed(ctx context.Context, texts []string) (EmbedResponse, error) {
	var out EmbedResponse
	err := c.postJSON(ctx, "/internal/v1/embeddings", map[string]any{"texts": texts}, &out)
	return out, err
}

// IsCode reports whether err is an AI service error with the given code.
func IsCode(err error, code string) bool {
	e, ok := err.(*Error)
	return ok && e.Code == code
}

// VectorLiteral formats a vector for pgvector's text input ("[1,2,3]").
func VectorLiteral(v []float64) string {
	var b strings.Builder
	b.Grow(len(v) * 10)
	b.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(x, 'g', 7, 32))
	}
	b.WriteByte(']')
	return b.String()
}
