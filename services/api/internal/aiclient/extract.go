package aiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Error is a failure reported by (or while reaching) the AI service.
// Retryable distinguishes transient faults (network, 5xx, provider overload)
// from permanent ones (unreadable document, provider not configured, refusal).
type Error struct {
	Status    int
	Code      string
	Message   string
	Retryable bool
}

func (e *Error) Error() string {
	return fmt.Sprintf("ai service: %s (%d): %s", e.Code, e.Status, e.Message)
}

// permanentCodes are failures that retrying cannot fix.
var permanentCodes = map[string]bool{
	"AI_PROVIDER_UNAVAILABLE": true, // no/invalid credentials: needs an operator
	"AI_PROVIDER_REJECTED":    true, // provider returned 400 for this input
	"AI_REFUSED":              true, // provider declined (safety)
	"UNREADABLE_DOCUMENT":     true,
	"DOCUMENT_TOO_LONG":       true,
}

type ExtractCVRequest struct {
	Filename      string `json:"filename"`
	MimeType      string `json:"mime_type"`
	ContentBase64 string `json:"content_base64"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type CallMeta struct {
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	PromptVersion string `json:"prompt_version"`
	Usage         Usage  `json:"usage"`
	LatencyMS     int    `json:"latency_ms"`
}

// ExtractCVResponse keeps the proposal as raw JSON: the platform stores it for
// user review and does not interpret it until the user applies items.
type ExtractCVResponse struct {
	Proposal json.RawMessage `json:"proposal"`
	Meta     CallMeta        `json:"meta"`
}

// ExtractCV asks the AI service to propose profile facts from a CV.
func (c *Client) ExtractCV(ctx context.Context, in ExtractCVRequest) (ExtractCVResponse, error) {
	var out ExtractCVResponse
	err := c.postJSON(ctx, "/internal/v1/cv/extract", in, &out)
	return out, err
}

func (c *Client) postJSON(ctx context.Context, path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		// Network failures and timeouts reaching the AI service are transient.
		return &Error{Status: 0, Code: "AI_SERVICE_UNREACHABLE", Message: err.Error(), Retryable: true}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return &Error{Status: resp.StatusCode, Code: "AI_SERVICE_READ_FAILED", Message: err.Error(), Retryable: true}
	}
	if resp.StatusCode != http.StatusOK {
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &env)
		e := &Error{Status: resp.StatusCode, Code: env.Error.Code, Message: env.Error.Message}
		if e.Code == "" {
			e.Code = "AI_SERVICE_ERROR"
		}
		// 5xx and 429 are transient unless the service says the provider is
		// not configured (a permanent condition until an operator acts).
		e.Retryable = (resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests) && !permanentCodes[e.Code]
		return e
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &Error{Status: resp.StatusCode, Code: "AI_SERVICE_BAD_RESPONSE", Message: err.Error()}
	}
	return nil
}
