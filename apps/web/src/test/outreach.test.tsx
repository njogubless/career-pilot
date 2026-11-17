import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { mockApi, renderApp, signedIn, type Reply } from "./render";

const now = new Date().toISOString();
const gmailConnected = { configured: true, account: { email: "ada@gmail.test", scopes: [], status: "CONNECTED", last_error: null, connected_at: now }, scopes_requested: [] };

const contact = {
  id: "c1",
  name: "",
  title: "",
  company: "Acme",
  job_id: "j1",
  profile_url: "",
  email: "recruiting@acme.test",
  source: "JOB_POSTING",
  source_evidence: "Questions about the role? Email recruiting@acme.test.",
  email_verification: "DOMAIN_OK",
  verified_at: now,
  confidence: "MEDIUM",
  do_not_contact: false,
  do_not_contact_reason: "",
  notes: "",
  discovered_at: now,
  updated_at: now,
  messages_sent: 0,
  last_contacted_at: null,
};

const message = (over: Record<string, unknown> = {}) => ({
  id: "m1",
  contact_id: "c1",
  contact_name: "",
  job_id: "j1",
  application_id: null,
  parent_id: null,
  kind: "INTRODUCTION",
  status: "DRAFT",
  origin: "TEMPLATE",
  to_email: "recruiting@acme.test",
  subject: "Interest in Staff Engineer role at Acme",
  body: "Hello,\n\nI'm writing about the Staff Engineer role at Acme.\n\nBest regards,\nAda",
  attachment_version_id: null,
  validation: { validator_version: "fact-check-2", status: "PASSED", issues: [], claims_checked: 3 },
  approved_at: null,
  gmail_thread_id: null,
  sent_at: null,
  error_message: null,
  replied_at: null,
  prompt_version: null,
  created_at: now,
  updated_at: now,
  replies: [],
  follow_ups: [],
  ...over,
});

const base = {
  ...signedIn,
  "GET /api/v1/application-documents": [200, { data: [] }] as Reply,
} satisfies Record<string, Reply>;

describe("recruiter outreach", () => {
  it("says honestly when Gmail is not configured and shows where contacts came from", async () => {
    mockApi({
      ...base,
      "GET /api/v1/integrations/gmail": [200, { configured: false, account: null, scopes_requested: [] }],
      "GET /api/v1/outreach/messages": [200, { data: [] }],
      "GET /api/v1/contacts": [200, { data: [contact] }],
    });
    const u = userEvent.setup();
    renderApp("/outreach");
    expect(await screen.findByText(/Gmail is not configured on this server/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Check for replies" })).toBeDisabled();
    await u.click(screen.getByRole("tab", { name: /Contacts/ }));
    expect(await screen.findByText("recruiting@acme.test")).toBeInTheDocument();
    expect(screen.getByText("From the posting")).toBeInTheDocument();
    expect(screen.getByText("Domain accepts mail")).toBeInTheDocument();
    expect(screen.getByText(/“Questions about the role\?/)).toBeInTheDocument();
  });

  it("requires explicit approval, then sends with an idempotency key", async () => {
    let status = "DRAFT";
    const calls = mockApi({
      ...base,
      "GET /api/v1/integrations/gmail": [200, gmailConnected],
      "GET /api/v1/outreach/messages/m1": () => [200, message({ status })],
      "POST /api/v1/outreach/messages/m1/approve": () => {
        status = "APPROVED";
        return [200, message({ status, approved_at: now })];
      },
      "POST /api/v1/outreach/messages/m1/send": () => {
        status = "SENT";
        return [200, message({ status, sent_at: now, gmail_thread_id: "t1" })];
      },
    });
    const u = userEvent.setup();
    renderApp("/outreach/messages/m1");
    const approve = await screen.findByRole("button", { name: "Approve email" });
    expect(approve).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Send now" })).not.toBeInTheDocument();
    await u.click(screen.getByLabelText(/I have read this email and want it sent/));
    await u.click(approve);
    await waitFor(() => expect(calls.some((c) => c.url.endsWith("/approve"))).toBe(true));
    expect(calls.find((c) => c.url.endsWith("/approve"))!.body).toEqual({ confirm: true, acknowledge_issues: false });
    await u.click(await screen.findByRole("button", { name: "Send now" }));
    await waitFor(() => expect(calls.some((c) => c.url.endsWith("/send"))).toBe(true));
    expect(calls.find((c) => c.url.endsWith("/send"))!.headers.get("Idempotency-Key")).toMatch(/^web-/);
    expect(await screen.findByText(/Sent .* from your Gmail/)).toBeInTheDocument();
  });

  it("requires acknowledging fact-check errors and explains unknown outcomes", async () => {
    mockApi({
      ...base,
      "GET /api/v1/integrations/gmail": [200, gmailConnected],
      "GET /api/v1/outreach/messages/m1": [200, message({
        validation: { validator_version: "x", status: "FAILED", claims_checked: 2, issues: [{ block_id: "p2", code: "UNSUPPORTED_NUMBER", severity: "ERROR", message: "The figure 45 does not appear in the cited profile facts.", excerpt: "45" }] },
      })],
      "GET /api/v1/outreach/messages/m2": [200, message({ id: "m2", status: "UNKNOWN", error_message: "Gmail did not confirm the send. Check your Gmail Sent folder before trying again." })],
    });
    const u = userEvent.setup();
    const { router } = renderApp("/outreach/messages/m1");
    expect(await screen.findByText("The figure 45 does not appear in the cited profile facts.")).toBeInTheDocument();
    await u.click(screen.getByLabelText(/I have read this email/));
    expect(screen.getByRole("button", { name: "Approve email" })).toBeDisabled();
    await u.click(screen.getByLabelText(/I have checked the 1 flagged statement/));
    expect(screen.getByRole("button", { name: "Approve email" })).toBeEnabled();
    await router.navigate("/outreach/messages/m2");
    expect(await screen.findByText(/Check your Gmail Sent folder/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "I checked: it was not sent" })).toBeInTheDocument();
  });

  it("finds contacts published in a job posting", async () => {
    const calls = mockApi({
      ...base,
      "GET /api/v1/jobs/j1": [200, {
        id: "j1", title: "Staff Engineer", company: "Acme", department: "", location: "Remote", remote_status: "REMOTE", remote_scope: "WORLDWIDE",
        remote_countries: [], remote_region: "", location_countries: [], employment_type: "FULL_TIME", salary: null, salary_text: "", posted_at: now,
        first_seen_at: now, last_seen_at: now, source: { kind: "GREENHOUSE", name: "Acme", attribution_required: false }, expired_at: null, match: null,
        description_html: "<p>x</p>", canonical_url: "https://example.com/j", apply_url: "https://example.com/apply", tags: [], duplicate_of: null, repost_of: null,
      }],
      "GET /api/v1/jobs/j1/match": [200, { status: "PROFILE_INCOMPLETE", match: null, analysis: { method: "NONE", analyzer_version: "", analyzed_at: null, model: null, deep_status: "NONE", deep_error: null, embedded: false }, feedback: null, deep_analysis_available: false, computed_at: null, disclaimer: "" }],
      "GET /api/v1/jobs/j1/application": [200, { application: null }],
      "GET /api/v1/contacts": [200, { data: [] }],
      "POST /api/v1/jobs/j1/contacts/discover": [200, { data: [{ contact, created: true }] }],
    });
    const u = userEvent.setup();
    renderApp("/jobs/j1");
    const panel = (await screen.findByRole("heading", { name: "Contacts" })).closest("section")!;
    await u.click(within(panel).getByRole("button", { name: "Find contacts in the posting" }));
    await waitFor(() => expect(calls.some((c) => c.url.endsWith("/contacts/discover"))).toBe(true));
    expect(await within(panel).findByText("1 new address(es) found in the posting.")).toBeInTheDocument();
  });

  it("connects Gmail through Google's consent screen", async () => {
    const assign = vi.fn();
    vi.stubGlobal("location", { ...window.location, assign });
    mockApi({
      ...signedIn,
      "GET /api/v1/automation/settings": [200, { mode: "HUMAN_APPROVED", paused: false, paused_at: null, daily_submission_limit: 20, updated_at: null }],
      "GET /api/v1/outreach/settings": [200, { daily_send_limit: 10, recipient_cooldown_days: 3, follow_ups_enabled: true, follow_up_after_days: 5, max_follow_ups: 1, signature: "" }],
      "GET /api/v1/integrations/gmail": [200, { configured: true, account: null, scopes_requested: [] }],
      "POST /api/v1/integrations/gmail/connect": [200, { auth_url: "https://accounts.google.com/o/oauth2/v2/auth?x=1" }],
    });
    const u = userEvent.setup();
    renderApp("/settings");
    expect(await screen.findByText(/send email, and read message headers to detect replies/)).toBeInTheDocument();
    await u.click(screen.getByRole("button", { name: "Connect Gmail" }));
    await waitFor(() => expect(assign).toHaveBeenCalledWith("https://accounts.google.com/o/oauth2/v2/auth?x=1"));
    vi.unstubAllGlobals();
  });
});
