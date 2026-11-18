package workflows

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/careerpilot/careerpilot/services/api/internal/aiclient"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
)

// Interpretation is an AI-proposed plan. It is shown to the user and only
// runs if they start it; the parameters already passed platform validation.
type Interpretation struct {
	Proposal      Proposal `json:"proposal"`
	Questions     []string `json:"questions"`
	Summary       string   `json:"summary"`
	PromptVersion string   `json:"prompt_version"`
}

func (e *Engine) Interpret(ctx context.Context, objective string) (Interpretation, error) {
	objective = strings.TrimSpace(objective)
	if len([]rune(objective)) < 3 || len([]rune(objective)) > 2000 {
		return Interpretation{}, httpx.Validation(map[string]string{"objective": "Describe your objective in 3 to 2000 characters."})
	}
	if e.ai == nil {
		return Interpretation{}, httpx.NewError(httpx.CodeUnavailable, "The AI service is not configured. Use the structured form instead.")
	}
	ictx, cancel := context.WithTimeout(ctx, 3*time.Second)
	info, err := e.ai.Info(ictx)
	cancel()
	if err != nil || !info.LLMConfigured || !info.Has("goals.interpret") {
		return Interpretation{}, httpx.NewError(httpx.CodeAIUnavailable, "No AI provider is configured, so objectives cannot be interpreted. Use the structured form instead.")
	}
	resp, err := e.ai.InterpretGoal(ctx, objective)
	if err != nil {
		var ae *aiclient.Error
		if errors.As(err, &ae) {
			return Interpretation{}, &httpx.Error{Code: httpx.CodeUnavailable, Message: "The objective could not be interpreted. Use the structured form instead.", Cause: err}
		}
		return Interpretation{}, err
	}
	// The model's parameters pass exactly the same validation as the form.
	// Nulls become defaults; unknown fields are rejected.
	var p map[string]any
	if err := json.Unmarshal(resp.Interpretation.Params, &p); err != nil {
		return Interpretation{}, httpx.NewError(httpx.CodeUnavailable, "The interpretation was malformed. Use the structured form instead.")
	}
	for k, v := range p {
		if v == nil {
			delete(p, k)
		}
	}
	clean, _ := json.Marshal(p)
	prop, err := Propose(resp.Interpretation.Template, clean)
	if err != nil {
		var he *httpx.Error
		if errors.As(err, &he) && he.Code == httpx.CodeValidationFailed {
			// Out-of-bounds proposals (e.g. "count 50") are clamped by asking, never run.
			return Interpretation{}, &httpx.Error{Code: httpx.CodeValidationFailed,
				Message: "The interpreted plan is outside what CareerPilot allows. Adjust it in the structured form.", Details: he.Details}
		}
		return Interpretation{}, err
	}
	qs := resp.Interpretation.Questions
	if qs == nil {
		qs = []string{}
	}
	return Interpretation{Proposal: prop, Questions: qs, Summary: resp.Interpretation.Summary, PromptVersion: resp.PromptVersion}, nil
}

type Summary struct {
	Workflow
	TasksTotal     int `json:"tasks_total"`
	TasksDone      int `json:"tasks_done"`
	PendingApprove int `json:"pending_approvals"`
}

func (e *Engine) List(ctx context.Context, userID uuid.UUID) ([]Summary, error) {
	rows, err := e.pool.Query(ctx, `SELECT `+prefixed("w.", wfCols)+`,
		(SELECT count(*) FROM workflow_tasks t WHERE t.workflow_id = w.id),
		(SELECT count(*) FROM workflow_tasks t WHERE t.workflow_id = w.id AND t.status IN ('SUCCEEDED','FAILED','SKIPPED','CANCELLED')),
		(SELECT count(*) FROM approval_requests a WHERE a.workflow_id = w.id AND a.status = 'PENDING')
		FROM workflow_executions w WHERE w.user_id = $1 ORDER BY w.created_at DESC LIMIT 100`, userID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Summary, error) {
		var s Summary
		var params, qs []byte
		err := r.Scan(&s.ID, &s.userID, &s.Template, &s.Objective, &s.Interpretation, &params, &s.PlanVersion, &s.Status, &s.StatusReason,
			&qs, &s.Report, &s.CreatedAt, &s.StartedAt, &s.CompletedAt, &s.UpdatedAt, &s.TasksTotal, &s.TasksDone, &s.PendingApprove)
		_ = json.Unmarshal(params, &s.Params)
		_ = json.Unmarshal(qs, &s.Questions)
		if s.Questions == nil {
			s.Questions = []string{}
		}
		return s, err
	})
	if out == nil {
		out = []Summary{}
	}
	return out, err
}

func prefixed(p, cols string) string {
	parts := strings.Split(cols, ",")
	for i, c := range parts {
		parts[i] = p + strings.TrimSpace(c)
	}
	return strings.Join(parts, ", ")
}

type Approval struct {
	ID          uuid.UUID       `json:"id"`
	WorkflowID  uuid.UUID       `json:"workflow_id"`
	TaskID      uuid.UUID       `json:"task_id"`
	Action      string          `json:"action"`
	Summary     string          `json:"summary"`
	Payload     json.RawMessage `json:"payload"`
	PayloadHash string          `json:"payload_hash"`
	Status      string          `json:"status"`
	Decision    json.RawMessage `json:"decision"`
	ExpiresAt   time.Time       `json:"expires_at"`
	DecidedAt   *time.Time      `json:"decided_at"`
	CreatedAt   time.Time       `json:"created_at"`
}

const approvalCols = `id, workflow_id, task_id, action, summary, payload, payload_hash, status, decision, expires_at, decided_at, created_at`

func scanApproval(r pgx.CollectableRow) (Approval, error) {
	var a Approval
	var hash []byte
	err := r.Scan(&a.ID, &a.WorkflowID, &a.TaskID, &a.Action, &a.Summary, &a.Payload, &hash, &a.Status, &a.Decision, &a.ExpiresAt, &a.DecidedAt, &a.CreatedAt)
	a.PayloadHash = hex.EncodeToString(hash)
	return a, err
}

func (e *Engine) Approvals(ctx context.Context, userID uuid.UUID, status string) ([]Approval, error) {
	rows, err := e.pool.Query(ctx, `SELECT `+approvalCols+` FROM approval_requests WHERE user_id = $1 AND ($2 = '' OR status = $2)
		ORDER BY created_at DESC LIMIT 100`, userID, status)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, scanApproval)
	if out == nil {
		out = []Approval{}
	}
	return out, err
}

type ToolCall struct {
	ID           uuid.UUID       `json:"id"`
	TaskID       uuid.UUID       `json:"task_id"`
	Agent        string          `json:"agent"`
	Tool         string          `json:"tool"`
	Attempt      int             `json:"attempt"`
	Outcome      string          `json:"outcome"`
	ErrorCode    *string         `json:"error_code"`
	ErrorMessage *string         `json:"error_message"`
	Summary      json.RawMessage `json:"summary"`
	DurationMS   int             `json:"duration_ms"`
	CreatedAt    time.Time       `json:"created_at"`
}

type Detail struct {
	Workflow
	Tasks     []Task     `json:"tasks"`
	Approvals []Approval `json:"approvals"`
	ToolCalls []ToolCall `json:"tool_calls"`
}

func (e *Engine) Get(ctx context.Context, userID, id uuid.UUID) (Detail, error) {
	w, err := scanWorkflow(e.pool.QueryRow(ctx, `SELECT `+wfCols+` FROM workflow_executions WHERE id = $1 AND user_id = $2`, id, userID))
	if err != nil {
		return Detail{}, err
	}
	d := Detail{Workflow: w}
	if d.Tasks, err = loadTasks(ctx, e.pool, id); err != nil {
		return Detail{}, err
	}
	rows, err := e.pool.Query(ctx, `SELECT `+approvalCols+` FROM approval_requests WHERE workflow_id = $1 ORDER BY created_at`, id)
	if err != nil {
		return Detail{}, err
	}
	if d.Approvals, err = pgx.CollectRows(rows, scanApproval); err != nil {
		return Detail{}, err
	}
	rows, err = e.pool.Query(ctx, `SELECT id, task_id, agent, tool, attempt, outcome, error_code, error_message, summary, duration_ms, created_at
		FROM agent_tool_calls WHERE workflow_id = $1 ORDER BY created_at, id`, id)
	if err != nil {
		return Detail{}, err
	}
	d.ToolCalls, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ToolCall, error) {
		var c ToolCall
		return c, r.Scan(&c.ID, &c.TaskID, &c.Agent, &c.Tool, &c.Attempt, &c.Outcome, &c.ErrorCode, &c.ErrorMessage, &c.Summary, &c.DurationMS, &c.CreatedAt)
	})
	return d, err
}
