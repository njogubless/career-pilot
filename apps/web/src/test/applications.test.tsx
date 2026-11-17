import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { mockApi, renderApp, signedIn, type Reply } from "./render";

const now = new Date().toISOString();
const automation = { mode: "HUMAN_APPROVED", paused: false, paused_at: null, daily_submission_limit: 20, updated_at: null };

const summary = (over: Record<string, unknown> = {}) => ({
  id: "a1",
  status: "SHORTLISTED",
  channel: null,
  job: { id: "j1", title: "Staff Engineer", company: "Stripe", location: "Remote", apply_url: "https://example.com/apply", expired_at: null },
  match: { score: 81.4, eligibility: "ELIGIBLE" },
  approved_documents: 0,
  pending_attempt: false,
  approved_at: null,
  submitted_at: null,
  created_at: now,
  updated_at: now,
  status_changed_at: now,
  ...over,
});

const detail = (over: Record<string, unknown> = {}) => ({
  ...summary(),
  notes: "",
  pinned: { cv_version_id: null, cover_letter_version_id: null, answers_version_id: null },
  package: {
    documents: [{ document_id: "d1", kind: "CV", approval_state: "CURRENT", approved_version_id: "v2", approved_version_no: 2, current_version_no: 2, approved_at: now }],
    ready: true,
    blockers: [],
    warnings: [],
  },
  allowed_transitions: [],
  channels: [
    { channel: "ASSISTED", available: true },
    { channel: "EMAIL", available: false, detail: "Email applications need a connected Gmail account, which arrives in Phase 7." },
  ],
  history: [{ from_status: null, to_status: "SHORTLISTED", actor: "USER", reason: "Created", created_at: now }],
  attempts: [],
  evidence: [],
  automation,
  ...over,
});

const stats = {
  by_status: {},
  active: 2,
  awaiting_approval: 1,
  interviewing: 0,
  offers: 0,
  submitted: 0,
  response_rate: null,
  median_days_to_response: null,
  automation_paused: false,
};

const pipelineApi = {
  ...signedIn,
  "GET /api/v1/applications": [200, { data: [summary(), summary({ id: "a2", status: "AWAITING_APPROVAL", job: { ...summary().job, id: "j2", title: "Platform Lead", company: "Datadog" } })] }] as Reply,
  "GET /api/v1/applications/stats": [200, stats] as Reply,
  "GET /api/v1/applications/activity": [200, { data: [{ application_id: "a2", job_title: "Platform Lead", company: "Datadog", from_status: "PREPARING", to_status: "AWAITING_APPROVAL", actor: "USER", reason: "", created_at: now }] }] as Reply,
  "GET /api/v1/automation/settings": [200, automation] as Reply,
} satisfies Record<string, Reply>;

describe("applications", () => {
  it("shows the pipeline with honest metrics and next steps", async () => {
    mockApi(pipelineApi);
    renderApp("/applications");
    const gate = await screen.findByRole("region", { name: "Approval gate" });
    expect(within(gate).getByText("Platform Lead")).toBeInTheDocument();
    expect(within(gate).getByText("Review and approve the package")).toBeInTheDocument();
    expect(within(screen.getByRole("region", { name: "Shortlisted" })).getByText("Staff Engineer")).toBeInTheDocument();
    // No submissions yet: the response rate is not invented.
    expect(screen.getByText("Measured once you have submissions")).toBeInTheDocument();
    expect(screen.getByText(/Submission channel:/)).toHaveTextContent("Assisted (you submit on the official site)");
    expect(screen.getByText("Preparing → Awaiting approval · user")).toBeInTheDocument();
  });

  it("requires explicit consent to approve and offers only available channels", async () => {
    const calls = mockApi({
      ...signedIn,
      "GET /api/v1/applications/a1": [200, detail({ status: "AWAITING_APPROVAL", allowed_transitions: ["PREPARING", "WITHDRAWN"] })],
      "POST /api/v1/applications/a1/approval": [200, detail({ status: "APPROVED", channel: "ASSISTED" })],
    });
    const u = userEvent.setup();
    renderApp("/applications/a1");
    const approve = await screen.findByRole("button", { name: "Approve application" });
    expect(approve).toBeDisabled();
    expect(screen.getByRole("radio", { name: /Email: send from my connected Gmail/ })).toBeDisabled();
    expect(screen.getByText(/arrives in Phase 7/)).toBeInTheDocument();
    await u.click(screen.getByLabelText(/authorise this application to be submitted/));
    await u.click(approve);
    await waitFor(() => expect(calls.some((c) => c.url.endsWith("/approval"))).toBe(true));
    expect(calls.find((c) => c.url.endsWith("/approval"))!.body).toEqual({ channel: "ASSISTED", confirm: true });
    expect(await screen.findByRole("heading", { name: "Submit on the official site" })).toBeInTheDocument();
  });

  it("starts an assisted submission with an idempotency key and records evidence only with attestation", async () => {
    const pending = { id: "t1", channel: "ASSISTED", actor: "USER", status: "PENDING", idempotency_key: "k", failure_reason: null, error_message: null, started_at: now, finished_at: null };
    let started = false;
    const calls = mockApi({
      ...signedIn,
      "GET /api/v1/applications/a1": () => [200, detail({ status: "APPROVED", channel: "ASSISTED", pinned: { cv_version_id: "v2", cover_letter_version_id: null, answers_version_id: null }, attempts: started ? [pending] : [] })],
      "POST /api/v1/applications/a1/submissions": () => {
        started = true;
        return [201, { attempt: pending, replayed: false }];
      },
      "POST /api/v1/applications/a1/submissions/t1/confirm": [200, detail({ status: "SUBMITTED" })],
    });
    const u = userEvent.setup();
    renderApp("/applications/a1");
    await u.click(await screen.findByRole("button", { name: "Start submission" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST" && c.url.endsWith("/submissions"))).toBe(true));
    const post = calls.find((c) => c.method === "POST" && c.url.endsWith("/submissions"))!;
    expect(post.headers.get("Idempotency-Key")).toMatch(/^web-/);

    expect(await screen.findByRole("link", { name: /Open the official application page/ })).toHaveAttribute("href", "https://example.com/apply");
    expect(screen.getByRole("link", { name: "DOCX" })).toHaveAttribute("href", "/api/v1/application-documents/d1/versions/v2/export?format=docx");
    await u.click(screen.getByRole("button", { name: "I submitted it" }));
    const record = screen.getByRole("button", { name: "Record submission" });
    expect(record).toBeDisabled();
    await u.type(screen.getByLabelText("Confirmation reference (optional)"), "GH-123");
    await u.click(screen.getByLabelText("I submitted this application on the employer's site."));
    await u.click(record);
    await waitFor(() => expect(calls.some((c) => c.url.endsWith("/confirm"))).toBe(true));
    expect(calls.find((c) => c.url.endsWith("/confirm"))!.body).toEqual({ attest: true, reference: "GH-123", note: "" });
  });

  it("explains verification failures and never offers to bypass them", async () => {
    const failed = { id: "t1", channel: "ASSISTED", actor: "USER", status: "FAILED", idempotency_key: "k", failure_reason: "VERIFICATION_REQUIRED", error_message: "The site required a human verification step (CAPTCHA, MFA or login).", started_at: now, finished_at: now };
    const calls = mockApi({
      ...signedIn,
      "GET /api/v1/applications/a1": [200, detail({ status: "FAILED", channel: "ASSISTED", attempts: [failed], allowed_transitions: ["APPROVED", "PREPARING", "WITHDRAWN"] })],
      "POST /api/v1/applications/a1/transitions": [200, detail({ status: "APPROVED" })],
    });
    const u = userEvent.setup();
    renderApp("/applications/a1");
    expect(await screen.findByText(/never bypasses CAPTCHAs/)).toBeInTheDocument();
    await u.click(screen.getByRole("button", { name: "Retry submission" }));
    await waitFor(() => expect(calls.some((c) => c.url.endsWith("/transitions"))).toBe(true));
    expect(calls.find((c) => c.url.endsWith("/transitions"))!.body).toEqual({ to: "APPROVED", reason: "Retrying submission" });
  });

  it("pauses all automation from Settings", async () => {
    const calls = mockApi({
      ...signedIn,
      "GET /api/v1/automation/settings": [200, automation],
      "POST /api/v1/automation/pause": [200, { ...automation, paused: true, paused_at: now }],
    });
    const u = userEvent.setup();
    renderApp("/settings");
    await u.click(await screen.findByRole("button", { name: "Pause all automation" }));
    await waitFor(() => expect(calls.some((c) => c.url === "/api/v1/automation/pause")).toBe(true));
    expect(await screen.findByRole("button", { name: "Resume automation" })).toBeInTheDocument();
  });
});
