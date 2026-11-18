// Package workflows is the central orchestrator (master prompt §15–21,
// ADR-002, ADR-024): user objectives become validated, template-based plans
// of tasks; specialised agents run their tasks only through tools they are
// explicitly granted; every tool call is validated, permission-checked,
// recorded and audited; state lives in Postgres and survives restarts.
package workflows

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/careerpilot/careerpilot/services/api/internal/automation"
)

// Side-effect classes (audit and permission level).
const (
	EffectRead     = "READ"     // reads platform data
	EffectInternal = "INTERNAL" // writes the user's own records (drafts, tracking)
	EffectExternal = "EXTERNAL" // acts outside the platform (none in Phase 8 templates)
)

// Error classes drive retry behaviour (AGENT_ARCHITECTURE §3).
const (
	ClassRetryable    = "RETRYABLE"
	ClassNonRetryable = "NON_RETRYABLE"
	ClassNeedsInput   = "NEEDS_INPUT"
	ClassDenied       = "DENIED"
)

// ToolError is the error contract every tool uses.
type ToolError struct {
	Class     string
	Code      string
	Message   string
	Questions []string
	Cause     error
}

func (e *ToolError) Error() string {
	if e.Cause != nil {
		return e.Code + ": " + e.Message + ": " + e.Cause.Error()
	}
	return e.Code + ": " + e.Message
}

func (e *ToolError) Unwrap() error { return e.Cause }

func retryable(code, msg string, cause error) *ToolError {
	return &ToolError{Class: ClassRetryable, Code: code, Message: msg, Cause: cause}
}

func permanent(code, msg string, cause error) *ToolError {
	return &ToolError{Class: ClassNonRetryable, Code: code, Message: msg, Cause: cause}
}

func needsInput(msg string, questions ...string) *ToolError {
	return &ToolError{Class: ClassNeedsInput, Code: "NEEDS_INPUT", Message: msg, Questions: questions}
}

// Input is a tool's static input: decoded strictly and validated before use.
type Input interface{ Validate() error }

// Tool is a registered capability with an explicit contract.
type Tool struct {
	Name        string
	Description string
	// Action is checked by automation.Guard immediately before the tool runs
	// ("" for read-only tools, which still require the workflow not be paused).
	Action      string
	Effect      string
	Timeout     time.Duration
	MaxAttempts int
	// Idempotency explains why re-running the tool is safe (recovery, retries).
	Idempotency string
	// Output describes the expected result (stored as the task's expected_output).
	Output   string
	NewInput func() Input
	Run      func(env *Env, in Input) (any, error)
}

// Agent is a logical specialised agent with an explicit tool allowlist.
type Agent struct {
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Tools       []string `json:"tools"`
}

var agents = map[string]Agent{
	"PROFILE_INTELLIGENCE": {Name: "PROFILE_INTELLIGENCE", Title: "Profile Intelligence",
		Description: "Reads verified profile facts and identifies missing information.", Tools: []string{"analyse_professional_profile"}},
	"DISCOVERY": {Name: "DISCOVERY", Title: "Discovery",
		Description: "Searches discovered jobs from approved sources with the objective's filters.", Tools: []string{"search_jobs"}},
	"MATCHING": {Name: "MATCHING", Title: "Matching",
		Description: "Scores suitability and eligibility, applies hard filters and ranks.", Tools: []string{"calculate_job_match", "rank_opportunities"}},
	"APPLICATION_PREPARATION": {Name: "APPLICATION_PREPARATION", Title: "Application Preparation",
		Description: "Prepares tailored CVs from verified facts and saves versions.", Tools: []string{"generate_tailored_cv"}},
	"COMPLIANCE": {Name: "COMPLIANCE", Title: "Compliance & Safety",
		Description: "Advisory checks: fact-check status, closed postings, eligibility, suspicious instructions in postings. Enforcement is deterministic.",
		Tools:       []string{"check_compliance"}},
	"EXECUTION": {Name: "EXECUTION", Title: "Execution",
		Description: "Updates application records through backend services and requests approvals. Never submits on third-party sites.",
		Tools:       []string{"update_application_status", "request_approval", "approve_application_package"}},
	"PERFORMANCE": {Name: "PERFORMANCE", Title: "Performance",
		Description: "Summarises outcomes into an execution report.", Tools: []string{"generate_career_report"}},
}

var tools = map[string]*Tool{}

func register(t *Tool) {
	if _, dup := tools[t.Name]; dup {
		panic("duplicate tool " + t.Name)
	}
	tools[t.Name] = t
}

// Grants reports whether agent may use tool. Agents can never expand their
// own permissions: grants are static code, not data.
func Grants(agent, tool string) bool {
	a, ok := agents[agent]
	return ok && slices.Contains(a.Tools, tool)
}

// decodeInput decodes strictly (unknown fields rejected) and validates.
func decodeInput(t *Tool, raw json.RawMessage) (Input, error) {
	in := t.NewInput()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(in); err != nil {
		return nil, fmt.Errorf("invalid input for %s: %w", t.Name, err)
	}
	if err := in.Validate(); err != nil {
		return nil, fmt.Errorf("invalid input for %s: %w", t.Name, err)
	}
	return in, nil
}

// ---- Public registry views (Automation Centre) ----

type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Action      string `json:"permission"`
	Effect      string `json:"effect"`
	TimeoutSec  int    `json:"timeout_seconds"`
	MaxAttempts int    `json:"max_attempts"`
	Idempotency string `json:"idempotency"`
	Output      string `json:"output"`
}

func Registry() ([]Agent, []ToolInfo) {
	as := make([]Agent, 0, len(agents))
	for _, a := range agents {
		as = append(as, a)
	}
	slices.SortFunc(as, func(a, b Agent) int { return compare(a.Title, b.Title) })
	ts := make([]ToolInfo, 0, len(tools))
	for _, t := range tools {
		ts = append(ts, ToolInfo{Name: t.Name, Description: t.Description, Action: t.Action, Effect: t.Effect,
			TimeoutSec: int(t.Timeout.Seconds()), MaxAttempts: t.MaxAttempts, Idempotency: t.Idempotency, Output: t.Output})
	}
	slices.SortFunc(ts, func(a, b ToolInfo) int { return compare(a.Name, b.Name) })
	return as, ts
}

func compare(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Permissions used by tools (automation guard actions).
var (
	permShortlist = automation.ActionShortlist
	permPrepare   = automation.ActionPrepare
)
