package workflows

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/careerpilot/careerpilot/services/api/internal/aiclient"
	"github.com/careerpilot/careerpilot/services/api/internal/applications"
	"github.com/careerpilot/careerpilot/services/api/internal/audit"
	"github.com/careerpilot/careerpilot/services/api/internal/automation"
	"github.com/careerpilot/careerpilot/services/api/internal/jobs"
	"github.com/careerpilot/careerpilot/services/api/internal/matching"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
	"github.com/careerpilot/careerpilot/services/api/internal/preparation"
	"github.com/careerpilot/careerpilot/services/api/internal/profiles"
)

const (
	maxActivePerUser       = 3
	approvalTTL            = 7 * 24 * time.Hour
	reasonAutomationPaused = "Automation is paused."
)

var activeStatuses = []string{"PENDING", "RUNNING", "NEEDS_INPUT", "AWAITING_APPROVAL", "PAUSED"}

type Engine struct {
	pool  *pgxpool.Pool
	queue *jobs.Client // API insert client; workers use the client from their context
	prof  *profiles.Service
	match *matching.Service
	apps  *applications.Service
	prep  *preparation.Service
	guard *automation.Guard
	ai    *aiclient.Client
	log   *slog.Logger
	now   func() time.Time
}

type Deps struct {
	Pool  *pgxpool.Pool
	Queue *jobs.Client
	Prof  *profiles.Service
	Match *matching.Service
	Apps  *applications.Service
	Prep  *preparation.Service
	Guard *automation.Guard
	AI    *aiclient.Client
	Log   *slog.Logger
}

func NewEngine(d Deps) *Engine {
	return &Engine{pool: d.Pool, queue: d.Queue, prof: d.Prof, match: d.Match, apps: d.Apps, prep: d.Prep, guard: d.Guard, ai: d.AI, log: d.Log, now: time.Now}
}

// Env is what a tool sees: the validated params and earlier results. Tools
// never receive raw external content as instructions.
type Env struct {
	Ctx        context.Context
	UserID     uuid.UUID
	WorkflowID uuid.UUID
	TaskID     uuid.UUID
	Params     Params
	Deps       map[string]json.RawMessage
	Tasks      []Task
	E          *Engine
}

// ---- River job ----

type TaskArgs struct {
	TaskID         uuid.UUID `json:"task_id"`
	UserID         uuid.UUID `json:"user_id"`
	TimeoutSeconds int       `json:"timeout_seconds"`
}

func (TaskArgs) Kind() string { return "workflow_task" }

func (e *Engine) enqueue(ctx context.Context, tx pgx.Tx, t Task) error {
	args := TaskArgs{TaskID: t.ID, UserID: t.UserID, TimeoutSeconds: t.TimeoutSeconds}
	opts := &river.InsertOpts{MaxAttempts: t.MaxAttempts}
	if c, err := river.ClientFromContextSafely[pgx.Tx](ctx); err == nil {
		_, err := c.InsertTx(ctx, tx, args, opts)
		return err
	}
	if e.queue == nil {
		return errors.New("no job queue available")
	}
	_, err := e.queue.InsertTx(ctx, tx, args, opts)
	return err
}

// ---- Model ----

type Workflow struct {
	ID             uuid.UUID       `json:"id"`
	Template       string          `json:"template"`
	Objective      string          `json:"objective"`
	Interpretation string          `json:"interpretation"`
	Params         Params          `json:"params"`
	PlanVersion    string          `json:"plan_version"`
	Status         string          `json:"status"`
	StatusReason   string          `json:"status_reason"`
	Questions      []string        `json:"questions"`
	Report         json.RawMessage `json:"report"`
	CreatedAt      time.Time       `json:"created_at"`
	StartedAt      *time.Time      `json:"started_at"`
	CompletedAt    *time.Time      `json:"completed_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	userID         uuid.UUID
}

type Task struct {
	ID                  uuid.UUID       `json:"id"`
	Seq                 int             `json:"seq"`
	Type                string          `json:"type"`
	Agent               string          `json:"agent"`
	Tool                string          `json:"tool"`
	Input               json.RawMessage `json:"input"`
	ExpectedOutput      string          `json:"expected_output"`
	RequiredPermissions []string        `json:"required_permissions"`
	Status              string          `json:"status"`
	DependsOn           []uuid.UUID     `json:"depends_on"`
	RunAlways           bool            `json:"run_always"`
	Attempts            int             `json:"attempts"`
	MaxAttempts         int             `json:"max_attempts"`
	TimeoutSeconds      int             `json:"timeout_seconds"`
	Result              json.RawMessage `json:"result"`
	ErrorCode           *string         `json:"error_code"`
	ErrorMessage        *string         `json:"error_message"`
	CreatedAt           time.Time       `json:"created_at"`
	StartedAt           *time.Time      `json:"started_at"`
	CompletedAt         *time.Time      `json:"completed_at"`
	WorkflowID          uuid.UUID       `json:"workflow_id"`
	UserID              uuid.UUID       `json:"-"`
}

const wfCols = `id, user_id, template, objective, interpretation, params, plan_version, status, status_reason, questions, report,
	created_at, started_at, completed_at, updated_at`

func scanWorkflow(row pgx.Row) (Workflow, error) {
	var w Workflow
	var params, qs []byte
	err := row.Scan(&w.ID, &w.userID, &w.Template, &w.Objective, &w.Interpretation, &params, &w.PlanVersion, &w.Status, &w.StatusReason,
		&qs, &w.Report, &w.CreatedAt, &w.StartedAt, &w.CompletedAt, &w.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return w, errNotFound
	}
	if err != nil {
		return w, err
	}
	_ = json.Unmarshal(params, &w.Params)
	_ = json.Unmarshal(qs, &w.Questions)
	if w.Questions == nil {
		w.Questions = []string{}
	}
	return w, nil
}

const taskCols = `id, workflow_id, user_id, seq, type, agent, tool, input, expected_output, required_permissions, status, depends_on,
	run_always, attempts, max_attempts, timeout_seconds, result, error_code, error_message, created_at, started_at, completed_at`

func scanTask(row pgx.Row) (Task, error) {
	var t Task
	err := row.Scan(&t.ID, &t.WorkflowID, &t.UserID, &t.Seq, &t.Type, &t.Agent, &t.Tool, &t.Input, &t.ExpectedOutput, &t.RequiredPermissions,
		&t.Status, &t.DependsOn, &t.RunAlways, &t.Attempts, &t.MaxAttempts, &t.TimeoutSeconds, &t.Result, &t.ErrorCode, &t.ErrorMessage,
		&t.CreatedAt, &t.StartedAt, &t.CompletedAt)
	return t, err
}

func loadTasks(ctx context.Context, q db.Querier, wfID uuid.UUID) ([]Task, error) {
	rows, err := q.Query(ctx, `SELECT `+taskCols+` FROM workflow_tasks WHERE workflow_id = $1 ORDER BY seq`, wfID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Task, error) { return scanTask(r) })
}

var errNotFound = httpx.NewError(httpx.CodeNotFound, "Workflow not found.")

func terminal(status string) bool {
	return status == "SUCCEEDED" || status == "FAILED" || status == "SKIPPED" || status == "CANCELLED"
}

// ---- Create ----

type CreateInput struct {
	Objective      string          `json:"objective"`
	Template       string          `json:"template"`
	Params         json.RawMessage `json:"params"`
	Interpretation string          `json:"interpretation"` // STRUCTURED | AI
}

// Create validates a plan and starts it. The plan is a fixed template whose
// parameters passed Params.Normalize; nothing an LLM proposed runs unchecked.
func (e *Engine) Create(ctx context.Context, userID uuid.UUID, in CreateInput, ip string) (uuid.UUID, error) {
	return e.create(ctx, userID, in, nil, ip)
}

// slot ties a scheduled run to its schedule and planned time.
type slot struct {
	ScheduleID uuid.UUID
	At         time.Time
}

var errSlotTaken = errors.New("schedule slot already has a run")

func (e *Engine) create(ctx context.Context, userID uuid.UUID, in CreateInput, sl *slot, ip string) (uuid.UUID, error) {
	if in.Interpretation == "" {
		in.Interpretation = "STRUCTURED"
	}
	if in.Interpretation != "STRUCTURED" && in.Interpretation != "AI" {
		return uuid.Nil, httpx.Validation(map[string]string{"interpretation": "STRUCTURED or AI."})
	}
	if len([]rune(in.Objective)) > 2000 {
		return uuid.Nil, httpx.Validation(map[string]string{"objective": "Use at most 2000 characters."})
	}
	prop, err := Propose(in.Template, in.Params)
	if err != nil {
		return uuid.Nil, err
	}
	wfID := ids.New()
	err = db.InTx(ctx, e.pool, func(tx pgx.Tx) error {
		// Serialise per user so the active-workflow limit holds under concurrency.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 8))`, userID); err != nil {
			return err
		}
		var active int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM workflow_executions WHERE user_id = $1 AND status = ANY($2)`, userID, activeStatuses).Scan(&active); err != nil {
			return err
		}
		if active >= maxActivePerUser {
			return httpx.NewError(httpx.CodeConflict, fmt.Sprintf("You already have %d workflows in progress. Finish or cancel one first.", active))
		}
		params, _ := json.Marshal(prop.Params)
		var schedID *uuid.UUID
		var schedAt *time.Time
		if sl != nil {
			schedID, schedAt = &sl.ScheduleID, &sl.At
		}
		if _, err := tx.Exec(ctx, `INSERT INTO workflow_executions (id, user_id, template, objective, interpretation, params, plan_version, status,
			started_at, schedule_id, scheduled_for) VALUES ($1,$2,$3,$4,$5,$6,$7,'RUNNING', now(), $8, $9)`,
			wfID, userID, prop.Template.Name, in.Objective, in.Interpretation, params, PlanVersion, schedID, schedAt); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return errSlotTaken
			}
			return err
		}
		byType := map[string]uuid.UUID{}
		for i, ts := range prop.Template.Tasks {
			tool := tools[ts.Tool]
			t := Task{ID: ids.New(), WorkflowID: wfID, UserID: userID, Seq: i + 1, Type: ts.Type, Agent: ts.Agent, Tool: ts.Tool,
				ExpectedOutput: tool.Output, RequiredPermissions: []string{}, Status: "PENDING", DependsOn: []uuid.UUID{}, RunAlways: ts.RunAlways,
				MaxAttempts: tool.MaxAttempts, TimeoutSeconds: int(tool.Timeout.Seconds())}
			if tool.Action != "" {
				t.RequiredPermissions = []string{tool.Action}
			}
			for _, d := range ts.DependsOn {
				t.DependsOn = append(t.DependsOn, byType[d])
			}
			t.Input, _ = json.Marshal(inputFor(ts.Type, prop.Params))
			byType[ts.Type] = t.ID
			if _, err := tx.Exec(ctx, `INSERT INTO workflow_tasks (id, workflow_id, user_id, seq, type, agent, tool, input, expected_output,
				required_permissions, status, depends_on, run_always, max_attempts, timeout_seconds) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
				t.ID, wfID, userID, t.Seq, t.Type, t.Agent, t.Tool, t.Input, t.ExpectedOutput, t.RequiredPermissions, t.Status, t.DependsOn,
				t.RunAlways, t.MaxAttempts, t.TimeoutSeconds); err != nil {
				return err
			}
		}
		actor := audit.ActorUser
		if sl != nil {
			actor = audit.ActorSystem // started by the user's schedule
		}
		if err := audit.Record(ctx, tx, audit.Event{UserID: &userID, Actor: actor, Action: "workflow.create", SubjectType: "workflow", SubjectID: &wfID,
			Outcome: audit.Success, IP: ip, Metadata: map[string]any{"template": prop.Template.Name, "interpretation": in.Interpretation,
				"plan_version": PlanVersion, "scheduled": sl != nil}}); err != nil {
			return err
		}
		return e.advance(ctx, tx, wfID)
	})
	return wfID, err
}

// advance makes tasks whose dependencies are done READY (enqueuing them in
// the same transaction), skips tasks blocked by failures, and finalises the
// workflow when nothing is left to do.
func (e *Engine) advance(ctx context.Context, tx pgx.Tx, wfID uuid.UUID) error {
	ts, err := loadTasks(ctx, tx, wfID)
	if err != nil {
		return err
	}
	status := map[uuid.UUID]string{}
	for _, t := range ts {
		status[t.ID] = t.Status
	}
	for changed := true; changed; {
		changed = false
		for i := range ts {
			t := &ts[i]
			if t.Status != "PENDING" {
				continue
			}
			allOK, allDone, blocked := true, true, false
			for _, d := range t.DependsOn {
				s := status[d]
				allOK = allOK && s == "SUCCEEDED"
				allDone = allDone && terminal(s)
				blocked = blocked || s == "FAILED" || s == "SKIPPED" || s == "CANCELLED"
			}
			next := ""
			switch {
			case allOK || (t.RunAlways && allDone):
				next = "READY"
			case blocked && !t.RunAlways:
				next = "SKIPPED"
			}
			if next == "" {
				continue
			}
			if _, err := tx.Exec(ctx, `UPDATE workflow_tasks SET status = $2, updated_at = now(),
				completed_at = CASE WHEN $2 = 'SKIPPED' THEN now() END WHERE id = $1`, t.ID, next); err != nil {
				return err
			}
			t.Status, status[t.ID], changed = next, next, true
			if next == "READY" {
				if err := e.enqueue(ctx, tx, *t); err != nil {
					return fmt.Errorf("enqueue task: %w", err)
				}
			}
		}
	}
	active, succeeded, unsuccessful := 0, 0, 0
	for _, t := range ts {
		switch {
		case !terminal(t.Status):
			active++
		case t.Status == "SUCCEEDED":
			succeeded++
		default:
			unsuccessful++
		}
	}
	if active > 0 {
		return nil
	}
	final := "SUCCEEDED"
	switch {
	case unsuccessful > 0 && succeeded > 1:
		final = "PARTIALLY_SUCCEEDED"
	case unsuccessful > 0:
		final = "FAILED"
	}
	var report []byte
	for _, t := range ts {
		if t.Type == "report" && t.Status == "SUCCEEDED" {
			report = t.Result
		}
	}
	var userID uuid.UUID
	if err := tx.QueryRow(ctx, `UPDATE workflow_executions SET status = $2, report = $3, completed_at = now(), updated_at = now()
		WHERE id = $1 AND status <> 'CANCELLED' RETURNING user_id`, wfID, final, report).Scan(&userID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	return audit.Record(ctx, tx, audit.Event{UserID: &userID, Actor: audit.ActorAgent, Action: "workflow.complete", SubjectType: "workflow",
		SubjectID: &wfID, Outcome: audit.Success, Metadata: map[string]any{"status": final}})
}

// ---- Task execution ----

type TaskWorker struct {
	river.WorkerDefaults[TaskArgs]
	Engine *Engine
}

func (w *TaskWorker) Timeout(job *river.Job[TaskArgs]) time.Duration {
	if job.Args.TimeoutSeconds > 0 {
		return time.Duration(job.Args.TimeoutSeconds)*time.Second + 30*time.Second
	}
	return 5 * time.Minute
}

func (w *TaskWorker) Work(ctx context.Context, job *river.Job[TaskArgs]) error {
	return w.Engine.runTask(ctx, job.Args.TaskID, job.Attempt, job.MaxAttempts)
}

// runTask executes one task. Phase 1 (transaction) claims it and checks the
// workflow and the kill switch; the tool runs outside any transaction;
// phase 2 (transaction) records the outcome and advances the workflow. A
// crash between phases re-runs the task, which is safe because tools are
// idempotent.
func (e *Engine) runTask(ctx context.Context, taskID uuid.UUID, attempt, maxAttempts int) error {
	var t Task
	var wf Workflow
	proceed := false
	err := db.InTx(ctx, e.pool, func(tx pgx.Tx) error {
		var err error
		if t, err = scanTask(tx.QueryRow(ctx, `SELECT `+taskCols+` FROM workflow_tasks WHERE id = $1 FOR UPDATE`, taskID)); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		if wf, err = scanWorkflow(tx.QueryRow(ctx, `SELECT `+wfCols+` FROM workflow_executions WHERE id = $1 FOR UPDATE`, t.WorkflowID)); err != nil {
			return err
		}
		if t.Status != "READY" && t.Status != "RUNNING" {
			return nil // finished, waiting, or cancelled meanwhile
		}
		if wf.Status == "CANCELLED" {
			_, err := tx.Exec(ctx, `UPDATE workflow_tasks SET status = 'CANCELLED', completed_at = now(), updated_at = now() WHERE id = $1`, t.ID)
			return err
		}
		if wf.Status == "PAUSED" {
			_, err := tx.Exec(ctx, `UPDATE workflow_tasks SET status = 'READY', updated_at = now() WHERE id = $1`, t.ID)
			return err
		}
		set, err := automation.Load(ctx, tx, t.UserID)
		if err != nil {
			return err
		}
		if set.Paused {
			// The kill switch stops everything an agent would do, reads included.
			if _, err := tx.Exec(ctx, `UPDATE workflow_tasks SET status = 'READY', updated_at = now() WHERE id = $1`, t.ID); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `UPDATE workflow_executions SET status = 'PAUSED', status_reason = $2, updated_at = now() WHERE id = $1`,
				wf.ID, reasonAutomationPaused)
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE workflow_tasks SET status = 'RUNNING', attempts = attempts + 1, started_at = coalesce(started_at, now()),
			updated_at = now() WHERE id = $1`, t.ID); err != nil {
			return err
		}
		t.Attempts++
		proceed = true
		return nil
	})
	if err != nil || !proceed {
		return err
	}

	start := e.now()
	result, runErr := e.execute(ctx, t, wf)
	dur := int(e.now().Sub(start).Milliseconds())

	var retryErr error
	err = db.InTx(context.WithoutCancel(ctx), e.pool, func(tx pgx.Tx) error {
		cur, err := scanWorkflow(tx.QueryRow(ctx, `SELECT `+wfCols+` FROM workflow_executions WHERE id = $1 FOR UPDATE`, t.WorkflowID))
		if err != nil {
			return err
		}
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM workflow_tasks WHERE id = $1 FOR UPDATE`, t.ID).Scan(&status); err != nil {
			return err
		}
		if cur.Status == "CANCELLED" || status != "RUNNING" {
			_, err := tx.Exec(ctx, `UPDATE workflow_tasks SET status = 'CANCELLED', completed_at = now(), updated_at = now()
				WHERE id = $1 AND status = 'RUNNING'`, t.ID)
			if err == nil {
				err = e.recordCall(ctx, tx, t, "FAILED", "CANCELLED", "The workflow was cancelled while this step ran; its result was discarded.", nil, dur)
			}
			return err
		}
		var te *ToolError
		var wait awaitApproval
		switch {
		case runErr == nil && isAwait(result, &wait):
			return e.openApproval(ctx, tx, t, cur, wait, dur)
		case runErr == nil:
			raw, err := json.Marshal(result)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE workflow_tasks SET status = 'SUCCEEDED', result = $2, error_code = NULL, error_message = NULL,
				completed_at = now(), updated_at = now() WHERE id = $1`, t.ID, raw); err != nil {
				return err
			}
			if err := e.recordCall(ctx, tx, t, "SUCCEEDED", "", "", summarize(result), dur); err != nil {
				return err
			}
			return e.advance(ctx, tx, t.WorkflowID)
		case errors.As(runErr, &te) && te.Class == ClassNeedsInput:
			qs, _ := json.Marshal(te.Questions)
			if _, err := tx.Exec(ctx, `UPDATE workflow_tasks SET status = 'NEEDS_INPUT', error_code = $2, error_message = $3, updated_at = now() WHERE id = $1`,
				t.ID, te.Code, te.Message); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE workflow_executions SET status = 'NEEDS_INPUT', status_reason = $2, questions = $3, updated_at = now() WHERE id = $1`,
				t.WorkflowID, te.Message, qs); err != nil {
				return err
			}
			return e.recordCall(ctx, tx, t, "NEEDS_INPUT", te.Code, te.Message, nil, dur)
		default:
			code, msg, class := "INTERNAL", "The step failed unexpectedly.", ClassRetryable
			if errors.As(runErr, &te) {
				code, msg, class = te.Code, te.Message, te.Class
			}
			outcome := "FAILED"
			if class == ClassDenied {
				outcome = "DENIED"
			}
			if err := e.recordCall(ctx, tx, t, outcome, code, msg, nil, dur); err != nil {
				return err
			}
			if class == ClassRetryable && attempt < maxAttempts {
				if _, err := tx.Exec(ctx, `UPDATE workflow_tasks SET error_code = $2, error_message = $3, updated_at = now() WHERE id = $1`,
					t.ID, code, msg+" Retrying."); err != nil {
					return err
				}
				retryErr = runErr // River retries with backoff
				return nil
			}
			if _, err := tx.Exec(ctx, `UPDATE workflow_tasks SET status = 'FAILED', error_code = $2, error_message = $3, completed_at = now(),
				updated_at = now() WHERE id = $1`, t.ID, code, msg); err != nil {
				return err
			}
			return e.advance(ctx, tx, t.WorkflowID)
		}
	})
	if err != nil {
		return err
	}
	if retryErr != nil {
		return retryErr
	}
	return nil
}

func isAwait(v any, out *awaitApproval) bool {
	a, ok := v.(awaitApproval)
	if ok {
		*out = a
	}
	return ok
}

// execute enforces the tool contract: grant, strict input, permission, timeout.
func (e *Engine) execute(ctx context.Context, t Task, wf Workflow) (any, error) {
	tool, ok := tools[t.Tool]
	if !ok {
		return nil, &ToolError{Class: ClassDenied, Code: "UNKNOWN_TOOL", Message: "The step uses an unregistered tool."}
	}
	if !Grants(t.Agent, t.Tool) {
		_ = audit.Record(ctx, e.pool, audit.Event{UserID: &t.UserID, Actor: audit.ActorAgent, Action: "workflow.tool." + t.Tool,
			SubjectType: "workflow_task", SubjectID: &t.ID, Outcome: audit.Denied, Metadata: map[string]any{"agent": t.Agent, "reason": "NOT_GRANTED"}})
		return nil, &ToolError{Class: ClassDenied, Code: "TOOL_NOT_GRANTED", Message: fmt.Sprintf("Agent %s is not allowed to use %s.", t.Agent, t.Tool)}
	}
	in, err := decodeInput(tool, t.Input)
	if err != nil {
		return nil, &ToolError{Class: ClassNonRetryable, Code: "INVALID_INPUT", Message: "The step's input failed validation.", Cause: err}
	}
	if tool.Action != "" {
		// Checked immediately before the tool runs; refusals are audited by the guard.
		if err := e.guard.Authorize(ctx, e.pool, t.UserID, audit.ActorAgent, tool.Action); err != nil {
			var d *automation.Denied
			if errors.As(err, &d) {
				return nil, &ToolError{Class: ClassDenied, Code: "PERMISSION_DENIED_" + d.Reason, Message: d.Message}
			}
			return nil, retryable("GUARD_UNAVAILABLE", "Permissions could not be checked.", err)
		}
	}
	all, err := loadTasks(ctx, e.pool, t.WorkflowID)
	if err != nil {
		return nil, retryable("STATE_UNAVAILABLE", "Workflow state could not be read.", err)
	}
	deps := map[string]json.RawMessage{}
	for _, x := range all {
		if x.Status == "SUCCEEDED" && x.Result != nil {
			deps[x.Type] = x.Result
		}
	}
	tctx, cancel := context.WithTimeout(ctx, tool.Timeout)
	defer cancel()
	out, err := tool.Run(&Env{Ctx: tctx, UserID: t.UserID, WorkflowID: t.WorkflowID, TaskID: t.ID, Params: wf.Params, Deps: deps, Tasks: all, E: e}, in)
	if err == nil && tctx.Err() != nil {
		err = retryable("TIMEOUT", "The step timed out.", tctx.Err())
	}
	return out, err
}

func (e *Engine) recordCall(ctx context.Context, tx pgx.Tx, t Task, outcome, code, msg string, summary map[string]any, dur int) error {
	if summary == nil {
		summary = map[string]any{}
	}
	s, _ := json.Marshal(summary)
	var codeP, msgP *string
	if code != "" {
		codeP, msgP = &code, &msg
	}
	if _, err := tx.Exec(ctx, `INSERT INTO agent_tool_calls (id, user_id, workflow_id, task_id, agent, tool, attempt, outcome, error_code, error_message,
		summary, duration_ms) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		ids.New(), t.UserID, t.WorkflowID, t.ID, t.Agent, t.Tool, t.Attempts, outcome, codeP, msgP, s, dur); err != nil {
		return err
	}
	auditOutcome := audit.Success
	switch outcome {
	case "FAILED":
		auditOutcome = audit.Failure
	case "DENIED":
		auditOutcome = audit.Denied
	}
	return audit.Record(ctx, tx, audit.Event{UserID: &t.UserID, Actor: audit.ActorAgent, Action: "workflow.tool." + t.Tool, SubjectType: "workflow_task",
		SubjectID: &t.ID, Outcome: auditOutcome, Metadata: map[string]any{"agent": t.Agent, "workflow_id": t.WorkflowID, "outcome": outcome, "code": code}})
}

// summarize keeps counts only (no content) for the tool-call log.
func summarize(v any) map[string]any {
	switch r := v.(type) {
	case SearchResult:
		return map[string]any{"jobs": len(r.Jobs), "total": r.Total}
	case MatchResult:
		return map[string]any{"scored": len(r.Scored), "unscored": r.Unscored}
	case RankResult:
		return map[string]any{"selected": len(r.Selected), "ineligible": r.Ineligible, "below_score": r.BelowScore}
	case ItemsResult:
		ok := 0
		for _, it := range r.Items {
			if it.OK {
				ok++
			}
		}
		return map[string]any{"items": len(r.Items), "ok": ok}
	case ApprovalOutcome:
		return map[string]any{"decision": r.Decision, "approved": len(r.Approved)}
	}
	return map[string]any{}
}

// ---- Approvals ----

func payloadHash(p ApprovalPayload) []byte {
	b, _ := json.Marshal(p)
	h := sha256.Sum256(b)
	return h[:]
}

func (e *Engine) openApproval(ctx context.Context, tx pgx.Tx, t Task, wf Workflow, wait awaitApproval, dur int) error {
	raw, _ := json.Marshal(wait.Payload)
	ok := 0
	for _, it := range wait.Payload.Items {
		if it.OK {
			ok++
		}
	}
	summary := fmt.Sprintf("Approve %d prepared application(s) for assisted submission", ok)
	if _, err := tx.Exec(ctx, `INSERT INTO approval_requests (id, user_id, workflow_id, task_id, action, summary, payload, payload_hash, status, expires_at)
		VALUES ($1,$2,$3,$4,'APPROVE_APPLICATION_PACKAGES',$5,$6,$7,'PENDING',$8)
		ON CONFLICT (task_id) DO UPDATE SET summary = EXCLUDED.summary, payload = EXCLUDED.payload, payload_hash = EXCLUDED.payload_hash,
			status = 'PENDING', decision = NULL, decided_at = NULL, expires_at = EXCLUDED.expires_at`,
		ids.New(), t.UserID, t.WorkflowID, t.ID, summary, raw, payloadHash(wait.Payload), e.now().Add(approvalTTL)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE workflow_tasks SET status = 'WAITING_APPROVAL', updated_at = now() WHERE id = $1`, t.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE workflow_executions SET status = 'AWAITING_APPROVAL', status_reason = $2, updated_at = now() WHERE id = $1`,
		wf.ID, summary); err != nil {
		return err
	}
	return e.recordCall(ctx, tx, t, "WAITING_APPROVAL", "", "", map[string]any{"approvable": ok}, dur)
}

type DecisionInput struct {
	Decision       string      `json:"decision"` // APPROVE | REJECT
	ApprovedJobIDs []uuid.UUID `json:"approved_job_ids"`
	// PayloadHash proves the decision is about what the user was shown.
	PayloadHash string `json:"payload_hash"`
}

func (e *Engine) Decide(ctx context.Context, userID, approvalID uuid.UUID, in DecisionInput, ip string) error {
	if in.Decision != "APPROVE" && in.Decision != "REJECT" {
		return httpx.Validation(map[string]string{"decision": "APPROVE or REJECT."})
	}
	return db.InTx(ctx, e.pool, func(tx pgx.Tx) error {
		var taskID, wfID uuid.UUID
		var status string
		var hash, raw []byte
		var expires time.Time
		err := tx.QueryRow(ctx, `SELECT task_id, workflow_id, status, payload_hash, payload, expires_at FROM approval_requests
			WHERE id = $1 AND user_id = $2 FOR UPDATE`, approvalID, userID).Scan(&taskID, &wfID, &status, &hash, &raw, &expires)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewError(httpx.CodeNotFound, "Approval request not found.")
		}
		if err != nil {
			return err
		}
		if status != "PENDING" || e.now().After(expires) {
			return httpx.NewError(httpx.CodeInvalidTransition, "This approval request is no longer open.")
		}
		if hex.EncodeToString(hash) != in.PayloadHash {
			return httpx.NewError(httpx.CodeConflict, "The request changed since you opened it. Reload and review it again.")
		}
		var p ApprovalPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		outcome := ApprovalOutcome{Decision: "REJECTED", Approved: []uuid.UUID{}}
		if in.Decision == "APPROVE" {
			if len(in.ApprovedJobIDs) == 0 {
				return httpx.Validation(map[string]string{"approved_job_ids": "Select at least one job, or reject the request."})
			}
			for _, id := range in.ApprovedJobIDs {
				ok := slices.ContainsFunc(p.Items, func(it Item) bool { return it.JobID == id && it.OK })
				if !ok {
					return httpx.Validation(map[string]string{"approved_job_ids": "Only jobs that passed the checks can be approved."})
				}
			}
			outcome = ApprovalOutcome{Decision: "APPROVED", Approved: in.ApprovedJobIDs}
		}
		res, _ := json.Marshal(outcome)
		if _, err := tx.Exec(ctx, `UPDATE approval_requests SET status = $2, decision = $3, decided_at = now() WHERE id = $1`,
			approvalID, map[string]string{"APPROVE": "APPROVED", "REJECT": "REJECTED"}[in.Decision], res); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE workflow_tasks SET status = 'SUCCEEDED', result = $2, completed_at = now(), updated_at = now()
			WHERE id = $1 AND status = 'WAITING_APPROVAL'`, taskID, res); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE workflow_executions SET status = 'RUNNING', status_reason = '', updated_at = now()
			WHERE id = $1 AND status = 'AWAITING_APPROVAL'`, wfID); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "workflow.approval.decide", SubjectType: "approval_request",
			SubjectID: &approvalID, Outcome: audit.Success, IP: ip, Metadata: map[string]any{"decision": in.Decision, "approved": len(outcome.Approved)}}); err != nil {
			return err
		}
		return e.advance(ctx, tx, wfID)
	})
}

// ---- Control: cancel, pause, resume, retry ----

func (e *Engine) lockOwned(ctx context.Context, tx pgx.Tx, userID, id uuid.UUID) (Workflow, error) {
	w, err := scanWorkflow(tx.QueryRow(ctx, `SELECT `+wfCols+` FROM workflow_executions WHERE id = $1 AND user_id = $2 FOR UPDATE`, id, userID))
	return w, err
}

func (e *Engine) Cancel(ctx context.Context, userID, id uuid.UUID, ip string) error {
	return db.InTx(ctx, e.pool, func(tx pgx.Tx) error {
		w, err := e.lockOwned(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		if !slices.Contains(activeStatuses, w.Status) {
			return httpx.NewError(httpx.CodeInvalidTransition, "This workflow has already finished.")
		}
		if _, err := tx.Exec(ctx, `UPDATE workflow_executions SET status = 'CANCELLED', status_reason = 'Cancelled by you.', completed_at = now(),
			updated_at = now() WHERE id = $1`, id); err != nil {
			return err
		}
		// Running steps finish their current call and then discard the result.
		if _, err := tx.Exec(ctx, `UPDATE workflow_tasks SET status = 'CANCELLED', completed_at = now(), updated_at = now()
			WHERE workflow_id = $1 AND status IN ('PENDING','READY','WAITING_APPROVAL','NEEDS_INPUT')`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE approval_requests SET status = 'CANCELLED', decided_at = now() WHERE workflow_id = $1 AND status = 'PENDING'`, id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "workflow.cancel", SubjectType: "workflow", SubjectID: &id, Outcome: audit.Success, IP: ip})
	})
}

func (e *Engine) Pause(ctx context.Context, userID, id uuid.UUID, ip string) error {
	return db.InTx(ctx, e.pool, func(tx pgx.Tx) error {
		w, err := e.lockOwned(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		if w.Status != "RUNNING" && w.Status != "PENDING" && w.Status != "AWAITING_APPROVAL" {
			return httpx.NewError(httpx.CodeInvalidTransition, "Only running workflows can be paused.")
		}
		if _, err := tx.Exec(ctx, `UPDATE workflow_executions SET status = 'PAUSED', status_reason = 'Paused by you.', updated_at = now() WHERE id = $1`, id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "workflow.pause", SubjectType: "workflow", SubjectID: &id, Outcome: audit.Success, IP: ip})
	})
}

// Resume continues a paused workflow, or one waiting for your input (the
// step that asked is re-run against your updated profile).
func (e *Engine) Resume(ctx context.Context, userID, id uuid.UUID, ip string) error {
	return db.InTx(ctx, e.pool, func(tx pgx.Tx) error {
		w, err := e.lockOwned(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		if w.Status != "PAUSED" && w.Status != "NEEDS_INPUT" {
			return httpx.NewError(httpx.CodeInvalidTransition, "Only paused workflows, or ones waiting for your input, can be resumed.")
		}
		set, err := automation.Load(ctx, tx, userID)
		if err != nil {
			return err
		}
		if set.Paused {
			return httpx.NewError(httpx.CodeInvalidTransition, "Automation is paused. Resume automation in Settings first.")
		}
		if err := e.resumeTx(ctx, tx, w); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "workflow.resume", SubjectType: "workflow", SubjectID: &id, Outcome: audit.Success, IP: ip})
	})
}

func (e *Engine) resumeTx(ctx context.Context, tx pgx.Tx, w Workflow) error {
	if _, err := tx.Exec(ctx, `UPDATE workflow_tasks SET status = 'READY', error_code = NULL, error_message = NULL, updated_at = now()
		WHERE workflow_id = $1 AND status = 'NEEDS_INPUT'`, w.ID); err != nil {
		return err
	}
	var waiting bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM workflow_tasks WHERE workflow_id = $1 AND status = 'WAITING_APPROVAL')`, w.ID).Scan(&waiting); err != nil {
		return err
	}
	status := "RUNNING"
	if waiting {
		status = "AWAITING_APPROVAL"
	}
	if _, err := tx.Exec(ctx, `UPDATE workflow_executions SET status = $2, status_reason = '', questions = '[]', updated_at = now() WHERE id = $1`, w.ID, status); err != nil {
		return err
	}
	ready, err := loadTasks(ctx, tx, w.ID)
	if err != nil {
		return err
	}
	for _, t := range ready {
		if t.Status == "READY" {
			if err := e.enqueue(ctx, tx, t); err != nil {
				return err
			}
		}
	}
	return e.advance(ctx, tx, w.ID)
}

// Retry re-runs failed and skipped steps of a finished workflow (partial
// failure recovery). Successful steps keep their results.
func (e *Engine) Retry(ctx context.Context, userID, id uuid.UUID, ip string) error {
	return db.InTx(ctx, e.pool, func(tx pgx.Tx) error {
		w, err := e.lockOwned(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		if w.Status != "FAILED" && w.Status != "PARTIALLY_SUCCEEDED" {
			return httpx.NewError(httpx.CodeInvalidTransition, "Only failed or partially successful workflows can be retried.")
		}
		var active int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM workflow_executions WHERE user_id = $1 AND status = ANY($2)`, userID, activeStatuses).Scan(&active); err != nil {
			return err
		}
		if active >= maxActivePerUser {
			return httpx.NewError(httpx.CodeConflict, "Too many workflows are in progress. Finish or cancel one first.")
		}
		if _, err := tx.Exec(ctx, `UPDATE workflow_tasks SET status = 'PENDING', attempts = 0, result = CASE WHEN run_always THEN NULL ELSE result END,
			error_code = NULL, error_message = NULL, started_at = NULL, completed_at = NULL, updated_at = now()
			WHERE workflow_id = $1 AND (status IN ('FAILED','SKIPPED') OR run_always)`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE workflow_executions SET status = 'RUNNING', status_reason = '', report = NULL, completed_at = NULL, updated_at = now()
			WHERE id = $1`, id); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "workflow.retry", SubjectType: "workflow", SubjectID: &id, Outcome: audit.Success, IP: ip}); err != nil {
			return err
		}
		return e.advance(ctx, tx, id)
	})
}

// ---- Maintenance (recovery) ----

// Maintain expires approvals, resumes workflows held by the global pause once
// it is lifted, and recovers tasks whose job was lost (for example after a
// crash exhausted River's attempts). It runs every minute in the worker.
func (e *Engine) Maintain(ctx context.Context) error {
	// Expired approvals fail their step; the report still runs.
	rows, err := e.pool.Query(ctx, `UPDATE approval_requests SET status = 'EXPIRED', decided_at = now()
		WHERE status = 'PENDING' AND expires_at < $1 RETURNING task_id, workflow_id`, e.now())
	if err != nil {
		return err
	}
	type pair struct{ task, wf uuid.UUID }
	expired, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (pair, error) {
		var p pair
		return p, r.Scan(&p.task, &p.wf)
	})
	if err != nil {
		return err
	}
	for _, p := range expired {
		if err := db.InTx(ctx, e.pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `UPDATE workflow_tasks SET status = 'FAILED', error_code = 'APPROVAL_EXPIRED',
				error_message = 'The approval request expired without a decision.', completed_at = now(), updated_at = now()
				WHERE id = $1 AND status = 'WAITING_APPROVAL'`, p.task); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE workflow_executions SET status = 'RUNNING', updated_at = now() WHERE id = $1 AND status = 'AWAITING_APPROVAL'`, p.wf); err != nil {
				return err
			}
			return e.advance(ctx, tx, p.wf)
		}); err != nil {
			return err
		}
	}

	// Lift holds caused by the global pause once automation is resumed.
	rows, err = e.pool.Query(ctx, `SELECT `+wfCols+` FROM workflow_executions w WHERE status = 'PAUSED' AND status_reason = $1
		AND NOT EXISTS (SELECT 1 FROM automation_settings a WHERE a.user_id = w.user_id AND a.paused)`, reasonAutomationPaused)
	if err != nil {
		return err
	}
	held, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Workflow, error) { return scanWorkflow(r) })
	if err != nil {
		return err
	}
	for _, w := range held {
		if err := db.InTx(ctx, e.pool, func(tx pgx.Tx) error {
			if _, err := e.lockOwned(ctx, tx, w.userID, w.ID); err != nil {
				return err
			}
			return e.resumeTx(ctx, tx, w)
		}); err != nil {
			return err
		}
	}

	// READY tasks with no live job (lost enqueue) are re-enqueued; RUNNING
	// tasks whose job is gone (attempts exhausted by crashes) are failed.
	rows, err = e.pool.Query(ctx, `SELECT `+taskCols+` FROM workflow_tasks t WHERE t.status IN ('READY','RUNNING') AND t.updated_at < $1
		AND EXISTS (SELECT 1 FROM workflow_executions w WHERE w.id = t.workflow_id AND w.status IN ('RUNNING','PENDING'))
		AND NOT EXISTS (SELECT 1 FROM river_job j WHERE j.kind = 'workflow_task' AND j.args->>'task_id' = t.id::text
			AND j.state IN ('available','scheduled','running','retryable','pending'))`, e.now().Add(-5*time.Minute))
	if err != nil {
		return err
	}
	orphans, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Task, error) { return scanTask(r) })
	if err != nil {
		return err
	}
	for _, t := range orphans {
		if err := db.InTx(ctx, e.pool, func(tx pgx.Tx) error {
			if t.Status == "READY" {
				return e.enqueue(ctx, tx, t)
			}
			if _, err := tx.Exec(ctx, `UPDATE workflow_tasks SET status = 'FAILED', error_code = 'INTERRUPTED',
				error_message = 'The step was interrupted and could not be completed. You can retry it.', completed_at = now(), updated_at = now()
				WHERE id = $1 AND status = 'RUNNING'`, t.ID); err != nil {
				return err
			}
			return e.advance(ctx, tx, t.WorkflowID)
		}); err != nil {
			return err
		}
	}
	return nil
}

type MaintainArgs struct{}

func (MaintainArgs) Kind() string { return "workflow_maintenance" }
func (MaintainArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 1, UniqueOpts: river.UniqueOpts{ByPeriod: 30 * time.Second}}
}

type MaintainWorker struct {
	river.WorkerDefaults[MaintainArgs]
	Engine *Engine
}

func (w *MaintainWorker) Work(ctx context.Context, _ *river.Job[MaintainArgs]) error {
	return w.Engine.Maintain(ctx)
}

func PeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(time.Minute),
			func() (river.JobArgs, *river.InsertOpts) { return MaintainArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}),
		river.NewPeriodicJob(river.PeriodicInterval(time.Minute),
			func() (river.JobArgs, *river.InsertOpts) { return ScheduleArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}),
	}
}
