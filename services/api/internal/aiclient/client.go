// Package aiclient is the typed client for the internal Python AI service
// (see docs/API_CONTRACTS.md §3 and ADR-006).
package aiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

func New(baseURL, token string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: timeout},
	}
}

// Info describes what the AI service can currently do.
type Info struct {
	Service        string   `json:"service"`
	Version        string   `json:"version"`
	LLMProvider    string   `json:"llm_provider"`    // "anthropic" | "openai" | "fake" | "none"
	LLMConfigured  bool     `json:"llm_configured"`  // a usable provider is configured
	PromptVersions []string `json:"prompt_versions"` // loaded prompt template ids
	Capabilities   []string `json:"capabilities"`    // implemented endpoints
}

// Health calls the unauthenticated liveness endpoint.
func (c *Client) Health(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/healthz", false, nil)
}

// Info returns the service's provider configuration.
func (c *Client) Info(ctx context.Context) (Info, error) {
	var out Info
	err := c.do(ctx, http.MethodGet, "/internal/v1/info", true, &out)
	return out, err
}

func (c *Client) do(ctx context.Context, method, path string, auth bool, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if auth {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("ai service %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("ai service %s: unexpected status %d", path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return fmt.Errorf("ai service %s: decode: %w", path, err)
	}
	return nil
}
