package aiclient

import (
	"context"
	"encoding/json"
)

// Fact is one verified profile fact sent to the AI service (doc-v1 contract).
type Fact struct {
	Ref    string            `json:"ref"`
	Kind   string            `json:"kind"`
	Text   string            `json:"text"`
	Fields map[string]string `json:"fields,omitempty"`
	Parent string            `json:"parent,omitempty"`
}

type Candidate struct {
	Name            string   `json:"name"`
	Email           string   `json:"email"`
	Location        string   `json:"location"`
	Links           []string `json:"links"`
	YearsExperience *float64 `json:"years_experience"`
	DegreeLevels    []string `json:"degree_levels"`
}

type JobContext struct {
	Title   string   `json:"title"`
	Company string   `json:"company"`
	Text    string   `json:"text"`
	Skills  []string `json:"skills"`
}

type Issue struct {
	BlockID  string `json:"block_id"`
	Code     string `json:"code"`
	Severity string `json:"severity"` // ERROR | WARNING
	Message  string `json:"message"`
	Excerpt  string `json:"excerpt"`
}

type ValidationReport struct {
	ValidatorVersion string  `json:"validator_version"`
	Status           string  `json:"status"` // PASSED | WARNINGS | FAILED
	Issues           []Issue `json:"issues"`
	ClaimsChecked    int     `json:"claims_checked"`
}

type GenerateDocumentRequest struct {
	Kind      string     `json:"kind"`
	Job       JobContext `json:"job"`
	Facts     []Fact     `json:"facts"`
	Candidate Candidate  `json:"candidate"`
	Questions []string   `json:"questions"`
}

type GenerateDocumentResponse struct {
	Content    json.RawMessage  `json:"content"`
	Validation ValidationReport `json:"validation"`
	Questions  []string         `json:"questions"`
	Warnings   []string         `json:"warnings"`
	Usage      AnalyzeUsage     `json:"usage"`
}

func (c *Client) GenerateDocument(ctx context.Context, in GenerateDocumentRequest) (GenerateDocumentResponse, error) {
	var out GenerateDocumentResponse
	err := c.postJSON(ctx, "/internal/v1/documents/generate", in, &out)
	return out, err
}

type ValidateDocumentRequest struct {
	Content   json.RawMessage `json:"content"`
	Facts     []Fact          `json:"facts"`
	Candidate Candidate       `json:"candidate"`
}

func (c *Client) ValidateDocument(ctx context.Context, in ValidateDocumentRequest) (ValidationReport, error) {
	var out ValidationReport
	err := c.postJSON(ctx, "/internal/v1/documents/validate", in, &out)
	return out, err
}

type OutreachContact struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Company string `json:"company"`
}

type OutreachDraftRequest struct {
	Kind      string          `json:"kind"` // INTRODUCTION | FOLLOW_UP
	Job       JobContext      `json:"job"`
	Facts     []Fact          `json:"facts"`
	Candidate Candidate       `json:"candidate"`
	Contact   OutreachContact `json:"contact"`
}

type OutreachDraftResponse struct {
	Subject    string           `json:"subject"`
	Greeting   string           `json:"greeting"`
	Paragraphs []string         `json:"paragraphs"`
	SignOff    string           `json:"sign_off"`
	Validation ValidationReport `json:"validation"`
	Questions  []string         `json:"questions"`
	Warnings   []string         `json:"warnings"`
	Usage      AnalyzeUsage     `json:"usage"`
}

func (c *Client) DraftOutreach(ctx context.Context, in OutreachDraftRequest) (OutreachDraftResponse, error) {
	var out OutreachDraftResponse
	err := c.postJSON(ctx, "/internal/v1/outreach/draft", in, &out)
	return out, err
}
