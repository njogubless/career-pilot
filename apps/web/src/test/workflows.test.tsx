import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { meta, mockApi, renderApp, signedIn, type Reply } from "./render";

const now = new Date().toISOString();
const templates = {
  plan_version: "plan-v1",
  data: [
    { name: "DISCOVER_AND_RANK", title: "Find and shortlist jobs", description: "", tasks: [{ type: "load_profile", title: "Review your verified profile", agent: "PROFILE_INTELLIGENCE", tool: "analyse_professional_profile", depends_on: [], run_always: false, approval: false }] },
    {
      name: "DISCOVER_PREPARE_APPROVE",
      title: "Find, prepare and ask me to approve",
      description: "",
      tasks: [
        { type: "load_profile", title: "Review your verified profile", agent: "PROFILE_INTELLIGENCE", tool: "analyse_professional_profile", depends_on: [], run_always: false, approval: false },
        { type: "request_approval", title: "Ask for your approval", agent: "EXECUTION", tool: "request_approval", depends_on: ["load_profile"], run_always: false, approval: true },
      ],
    },
  ],
};

const params = { query: "backend engineer", remote_only: true, country: "KE", min_monthly_salary: { amount: 4000, currency: "USD" }, require_disclosed_salary: false, posted_within_days: 30, count: 5, min_score: 50, use_ai: false };

const workflow = (over: Record<string, unknown> = {}) => ({
  id: "w1",
  template: "DISCOVER_PREPARE_APPROVE",
  objective: "Find five remote backend jobs",
  interpretation: "STRUCTURED",
  params,
  plan_version: "plan-v1",
  status: "RUNNING",
  status_reason: "",
  questions: [],
  report: null,
  created_at: now,
  started_at: now,
  completed_at: null,
  updated_at: now,
  tasks: [
    { id: "t1", seq: 1, type: "load_profile", agent: "PROFILE_INTELLIGENCE", tool: "analyse_professional_profile", expected_output: "", required_permissions: [], status: "SUCCEEDED", run_always: false, attempts: 1, max_attempts: 3, timeout_seconds: 30, error_code: null, error_message: null, started_at: now, completed_at: now },
    { id: "t2", seq: 2, type: "prepare_documents", agent: "APPLICATION_PREPARATION", tool: "generate_tailored_cv", expected_output: "", required_permissions: ["application.prepare"], status: "FAILED", run_always: false, attempts: 1, max_attempts: 2, timeout_seconds: 600, error_code: "PERMISSION_DENIED_MODE", error_message: "Automation is set to discovery only.", started_at: now, completed_at: now },
  ],
  approvals: [],
  tool_calls: [{ id: "c1", task_id: "t2", agent: "APPLICATION_PREPARATION", tool: "generate_tailored_cv", attempt: 1, outcome: "DENIED", error_code: "PERMISSION_DENIED_MODE", error_message: "Automation is set to discovery only.", summary: {}, duration_ms: 4, created_at: now }],
  ...over,
});

const approval = {
  id: "a1",
  workflow_id: "w1",
  task_id: "t9",
  action: "APPROVE_APPLICATION_PACKAGES",
  summary: "Approve 1 prepared application(s) for assisted submission",
  payload: {
    channel: "ASSISTED",
    note: "Approving a job approves its CV version and authorises an assisted submission.",
    items: [
      { job_id: "j1", title: "Senior Backend Engineer", company: "Acme", score: 82, document_id: "d1", validation: "PASSED", ok: true, flags: [] },
      { job_id: "j2", title: "Backend Engineer, Platform", company: "Acme", score: 75, document_id: "d2", validation: "PASSED", ok: false, flags: ["The posting contains instruction-like text aimed at automated systems."] },
    ],
  },
  payload_hash: "abc123",
  status: "PENDING",
  decision: null,
  expires_at: new Date(Date.now() + 6 * 86400_000).toISOString(),
  decided_at: null,
  created_at: now,
};

const pageApi = {
  ...signedIn,
  "GET /api/v1/workflows/templates": [200, templates] as Reply,
  "GET /api/v1/workflows": [200, { data: [] }] as Reply,
  "GET /api/v1/approvals": [200, { data: [] }] as Reply,
  "GET /api/v1/automation/settings": [200, { mode: "HUMAN_APPROVED", paused: false, paused_at: null, daily_submission_limit: 20, updated_at: null }] as Reply,
} satisfies Record<string, Reply>;

describe("intelligence centre", () => {
  it("starts a structured workflow without AI and shows the plan first", async () => {
    const calls = mockApi({ ...pageApi, "POST /api/v1/workflows": [201, workflow()], "GET /api/v1/workflows/w1": [200, workflow()] });
    const u = userEvent.setup();
    renderApp("/intelligence");
    expect(await screen.findByText("Ask for your approval")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Interpret with AI" })).toBeDisabled();
    await u.type(screen.getByLabelText("Role or keywords"), "backend engineer");
    await u.type(screen.getByLabelText("Country you can work in (ISO code)"), "ke");
    await u.click(screen.getByLabelText("Remote jobs only"));
    await u.click(screen.getByRole("button", { name: "Start workflow" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST" && c.url === "/api/v1/workflows")).toBe(true));
    const body = calls.find((c) => c.method === "POST")!.body as { template: string; interpretation: string; params: { query: string; country: string; remote_only: boolean; min_monthly_salary: unknown } };
    expect(body.template).toBe("DISCOVER_PREPARE_APPROVE");
    expect(body.interpretation).toBe("STRUCTURED");
    expect(body.params).toMatchObject({ query: "backend engineer", country: "KE", remote_only: true, min_monthly_salary: null });
    // The denial shows in the step list and in the tool-call audit.
    expect((await screen.findAllByText("Automation is set to discovery only.")).length).toBe(2);
  });

  it("fills the plan from an AI interpretation, which the user then starts", async () => {
    const calls = mockApi({
      ...pageApi,
      "GET /api/v1/meta": [200, { ...meta, integrations: { ...meta.integrations, llm: { status: "available" } } }],
      "POST /api/v1/workflows/interpret": [200, { proposal: { template: templates.data[1], params }, questions: ["Which seniority?"], summary: "Five remote backend jobs for Kenyan applicants.", prompt_version: "goal_interpret@1" }],
      "POST /api/v1/workflows": [201, workflow({ interpretation: "AI" })],
      "GET /api/v1/workflows/w1": [200, workflow({ interpretation: "AI" })],
    });
    const u = userEvent.setup();
    renderApp("/intelligence");
    await u.type(await screen.findByLabelText("Your objective"), "Find five remote backend jobs for Kenyans");
    await u.click(screen.getByRole("button", { name: "Interpret with AI" }));
    expect(await screen.findByText("Which seniority?")).toBeInTheDocument();
    expect(screen.getByLabelText("Country you can work in (ISO code)")).toHaveValue("KE");
    expect(calls.some((c) => c.method === "POST" && c.url === "/api/v1/workflows")).toBe(false); // nothing ran yet
    await u.click(screen.getByRole("button", { name: "Start workflow" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST" && c.url === "/api/v1/workflows")).toBe(true));
    expect(calls.find((c) => c.url === "/api/v1/workflows" && c.method === "POST")!.body).toMatchObject({ interpretation: "AI", params: { min_monthly_salary: { amount: 4000, currency: "USD" } } });
  });

  it("approves only checked items, bound to the payload hash", async () => {
    const calls = mockApi({
      ...signedIn,
      "GET /api/v1/workflows/w1": [200, workflow({ status: "AWAITING_APPROVAL", approvals: [approval] })],
      "POST /api/v1/approvals/a1/decision": [200, { status: "recorded" }],
    });
    const u = userEvent.setup();
    renderApp("/intelligence/workflows/w1");
    const panel = (await screen.findByText("Approve 1 prepared application(s) for assisted submission")).closest("section")!;
    expect(within(panel).getByLabelText("Approve Backend Engineer, Platform")).toBeDisabled();
    expect(within(panel).getByText(/instruction-like text/)).toBeInTheDocument();
    await u.click(within(panel).getByRole("button", { name: "Approve 1 selected" }));
    await waitFor(() => expect(calls.some((c) => c.url.endsWith("/decision"))).toBe(true));
    expect(calls.find((c) => c.url.endsWith("/decision"))!.body).toEqual({ decision: "APPROVE", approved_job_ids: ["j1"], payload_hash: "abc123" });
  });

  it("asks for input and offers retry after partial failure", async () => {
    const calls = mockApi({
      ...signedIn,
      "GET /api/v1/workflows/w1": [200, workflow({ status: "NEEDS_INPUT", questions: ["Add your skills, then resume."] })],
      "GET /api/v1/workflows/w2": [200, workflow({ id: "w2", status: "PARTIALLY_SUCCEEDED" })],
      "POST /api/v1/workflows/w1/resume": [200, workflow({ status: "RUNNING" })],
      "POST /api/v1/workflows/w2/retry": [200, workflow({ id: "w2", status: "RUNNING" })],
    });
    const u = userEvent.setup();
    const { router } = renderApp("/intelligence/workflows/w1");
    expect(await screen.findByText("Add your skills, then resume.")).toBeInTheDocument();
    await u.click(screen.getByRole("button", { name: "Resume" }));
    await waitFor(() => expect(calls.some((c) => c.url.endsWith("/w1/resume"))).toBe(true));
    await router.navigate("/intelligence/workflows/w2");
    await u.click(await screen.findByRole("button", { name: "Retry failed steps" }));
    await waitFor(() => expect(calls.some((c) => c.url.endsWith("/w2/retry"))).toBe(true));
  });

  it("shows exactly what each agent may do in the Automation Centre", async () => {
    mockApi({
      ...signedIn,
      "GET /api/v1/automation/settings": [200, { mode: "HUMAN_APPROVED", paused: false, paused_at: null, daily_submission_limit: 20, updated_at: null }],
      "GET /api/v1/agents": [200, {
        agents: [{ name: "APPLICATION_PREPARATION", title: "Application Preparation", description: "Prepares tailored CVs.", tools: ["generate_tailored_cv"] }],
        tools: [{ name: "generate_tailored_cv", description: "", permission: "application.prepare", effect: "INTERNAL", timeout_seconds: 600, max_attempts: 2, idempotency: "", output: "" }],
      }],
    });
    renderApp("/automation");
    expect(await screen.findByText("generate_tailored_cv")).toBeInTheDocument();
    expect(screen.getByText("Needs application.prepare")).toBeInTheDocument();
    expect(screen.getByText(/No agent can send email or submit applications/)).toBeInTheDocument();
  });
});
