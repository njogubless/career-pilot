import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { meta, mockApi, renderApp, signedIn, type Reply } from "./render";

const now = new Date().toISOString();

const content = {
  schema_version: "doc-v1",
  kind: "CV",
  sections: [
    { key: "contact", title: "Contact", blocks: [{ id: "b1", type: "contact", text: "Amani Otieno", refs: [], meta: { email: "amani@example.com" } }] },
    {
      key: "experience",
      title: "Experience",
      blocks: [
        { id: "b2", type: "entry", text: "Senior Engineer — Acme", refs: ["exp:1"], meta: { dates: "Jan 2020 – Present" } },
        { id: "b3", type: "bullet", text: "Cut ledger costs by 99%.", refs: ["exp:1#a1"], meta: { parent: "b2" } },
      ],
    },
  ],
};

const version = (over: Record<string, unknown> = {}) => ({
  id: "v1",
  version_no: 1,
  origin: "GENERATED",
  restored_from: null,
  validation_status: "FAILED",
  prompt_version: "cv_tailor@1",
  model: "claude-opus-5-5",
  created_at: now,
  content,
  validation: {
    validator_version: "fact-check-1+tax-3",
    status: "FAILED",
    claims_checked: 1,
    issues: [{ block_id: "b3", code: "UNSUPPORTED_NUMBER", severity: "ERROR", message: "The figure 99 does not appear in the cited profile facts.", excerpt: "99" }],
  },
  questions: ["What was the size of the ledger?"],
  warnings: [],
  facts_changed: false,
  ...over,
});

const doc = (over: Record<string, unknown> = {}) => ({
  id: "d1",
  job_id: "j1",
  kind: "CV",
  title: "Tailored CV",
  notes: "",
  questions: [],
  current_version_id: "v1",
  approved_version_id: null,
  approved_at: null,
  approval_state: "NONE",
  job_title: "Staff Engineer",
  company: "Stripe",
  created_at: now,
  updated_at: now,
  current_version: version(),
  latest_generation: { id: "g1", status: "SUCCEEDED", version_id: "v1", error_code: null, error_message: null, created_at: now, updated_at: now },
  facts: [{ ref: "exp:1#a1", kind: "achievement", text: "Cut ledger costs by 30%." }],
  ...over,
});

const editorApi = {
  ...signedIn,
  "GET /api/v1/application-documents/d1": [200, doc()] as Reply,
  "GET /api/v1/application-documents/d1/versions": [200, { data: [version()] }] as Reply,
} satisfies Record<string, Reply>;

describe("application preparation", () => {
  it("shows profile-rendered structure read-only, flags unsupported claims and the evidence", async () => {
    mockApi(editorApi);
    renderApp("/prepare/d1");
    expect(await screen.findByRole("heading", { name: "Tailored CV" })).toBeInTheDocument();
    // Entry headings come from the profile and are not editable.
    expect(screen.getByText("Senior Engineer — Acme")).toBeInTheDocument();
    expect(screen.queryByDisplayValue("Senior Engineer — Acme")).not.toBeInTheDocument();
    expect(screen.getByDisplayValue("Cut ledger costs by 99%.")).toBeInTheDocument();
    expect(screen.getByText("The figure 99 does not appear in the cited profile facts.")).toBeInTheDocument();
    expect(screen.getByText("Cut ledger costs by 30%.")).toBeInTheDocument(); // cited fact
    expect(screen.getByText("What was the size of the ledger?")).toBeInTheDocument();
    expect(screen.getAllByText("Unsupported statements").length).toBeGreaterThan(0);
  });

  it("saves edits as a new version against the base version", async () => {
    const calls = mockApi({ ...editorApi, "PUT /api/v1/application-documents/d1/content": [200, version({ id: "v2", version_no: 2, origin: "USER_EDIT" })] });
    const u = userEvent.setup();
    renderApp("/prepare/d1");
    const box = await screen.findByDisplayValue("Cut ledger costs by 99%.");
    const save = screen.getByRole("button", { name: "Save as new version" });
    expect(save).toBeDisabled();
    await u.clear(box);
    await u.type(box, "Cut ledger costs by 30%.");
    expect(screen.getByText("Unsaved changes")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Approve version 1" })).toBeDisabled();
    await u.click(save);
    await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true));
    const put = calls.find((c) => c.method === "PUT")!;
    const body = put.body as { base_version_id: string; content: typeof content };
    expect(body.base_version_id).toBe("v1");
    expect(body.content.sections[1].blocks[1].text).toBe("Cut ledger costs by 30%.");
  });

  it("requires explicit acknowledgement to approve with fact-check errors", async () => {
    const calls = mockApi({ ...editorApi, "POST /api/v1/application-documents/d1/approval": [200, { document: doc({ approval_state: "CURRENT" }), validation: version().validation }] });
    const u = userEvent.setup();
    renderApp("/prepare/d1");
    const approve = await screen.findByRole("button", { name: "Approve version 1" });
    expect(approve).toBeDisabled();
    await u.click(screen.getByLabelText(/I have checked the 1 flagged statement/));
    expect(approve).toBeEnabled();
    await u.click(approve);
    await waitFor(() => expect(calls.some((c) => c.method === "POST" && c.url.endsWith("/approval"))).toBe(true));
    expect(calls.find((c) => c.url.endsWith("/approval"))!.body).toEqual({ version_id: "v1", acknowledge_issues: true });
  });

  it("blocks approval while an answer still needs the user's input", async () => {
    const answers = {
      schema_version: "doc-v1",
      kind: "APPLICATION_ANSWERS",
      sections: [{ key: "q1", title: "Salary expectations?", blocks: [{ id: "b1", type: "answer", text: "", refs: [], meta: { status: "NEEDS_INPUT", question_for_user: "Please state your expected salary." } }] }],
    };
    const v = version({
      content: answers,
      validation: { validator_version: "x", status: "FAILED", claims_checked: 1, issues: [{ block_id: "b1", code: "NEEDS_INPUT", severity: "ERROR", message: "Please state your expected salary.", excerpt: "" }] },
      questions: [],
    });
    mockApi({ ...editorApi, "GET /api/v1/application-documents/d1": [200, doc({ kind: "APPLICATION_ANSWERS", current_version: v })] });
    renderApp("/prepare/d1");
    expect(await screen.findByText("1 part(s) still need your input. Complete or remove them before approving.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Approve version 1" })).toBeDisabled();
    expect(screen.queryByLabelText(/I have checked/)).not.toBeInTheDocument();
  });

  it("reports a failed generation honestly with a path to fix it", async () => {
    mockApi({
      ...signedIn,
      "GET /api/v1/application-documents/d1": [200, doc({
        current_version_id: null,
        current_version: null,
        latest_generation: { id: "g1", status: "FAILED", version_id: null, error_code: "PROFILE_INCOMPLETE", error_message: "Your profile has no verified experience, skills or education to build from.", created_at: now, updated_at: now },
      })],
    });
    renderApp("/prepare/d1");
    expect(await screen.findByText(/Your profile has no verified experience/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open your profile" })).toHaveAttribute("href", "/profile");
  });

  it("starts generation from the job page with questions and notes", async () => {
    const calls = mockApi({
      ...jobPageApi,
      "GET /api/v1/meta": [200, { ...meta, integrations: { ...meta.integrations, llm: { status: "available", detail: "anthropic" } } }],
      "POST /api/v1/jobs/j1/application-documents": [202, { document: doc({ current_version_id: null }), generation: { id: "g1", status: "QUEUED" } }],
    });
    const u = userEvent.setup();
    renderApp("/jobs/j1");
    const panel = (await screen.findByRole("heading", { name: "Tailored documents" })).closest("section")!;
    const rows = within(panel).getAllByRole("listitem");
    await u.click(within(rows[2]).getByRole("button", { name: "Generate" }));
    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getByText(/use only/)).toBeInTheDocument();
    await u.type(within(dialog).getByLabelText("Application questions (one per line)"), "Why us?{Enter}Describe a system you built.");
    await u.type(within(dialog).getByLabelText("Notes for this application (optional)"), "I use Stripe daily.");
    await u.click(await within(dialog).findByRole("button", { name: "Generate with AI" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    expect(calls.find((c) => c.method === "POST")!.body).toEqual({
      kind: "APPLICATION_ANSWERS",
      source: "AI",
      notes: "I use Stripe daily.",
      questions: ["Why us?", "Describe a system you built."],
    });
    expect(await screen.findByRole("heading", { name: "Tailored CV" })).toBeInTheDocument(); // navigated to the editor
  });

  it("offers starting from the profile when no AI provider is configured", async () => {
    const calls = mockApi({
      ...jobPageApi,
      "POST /api/v1/jobs/j1/application-documents": [201, { document: doc(), generation: null }],
    });
    const u = userEvent.setup();
    renderApp("/jobs/j1");
    const panel = (await screen.findByRole("heading", { name: "Tailored documents" })).closest("section")!;
    await u.click(within(within(panel).getAllByRole("listitem")[0]).getByRole("button", { name: "Generate" }));
    const dialog = screen.getByRole("dialog");
    expect(await within(dialog).findByText(/AI drafts are unavailable/)).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Generate with AI" })).toBeDisabled();
    await u.click(within(dialog).getByRole("button", { name: "Start from profile" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    expect(calls.find((c) => c.method === "POST")!.body).toEqual({ kind: "CV", source: "PROFILE", notes: "" });
  });
});

const jobPageApi = {
  ...signedIn,
  "GET /api/v1/jobs/j1": [200, {
    id: "j1", title: "Staff Engineer", company: "Stripe", department: "", location: "Remote", remote_status: "REMOTE", remote_scope: "WORLDWIDE",
    remote_countries: [], remote_region: "", location_countries: [], employment_type: "FULL_TIME", salary: null, salary_text: "", posted_at: now,
    first_seen_at: now, last_seen_at: now, source: { kind: "GREENHOUSE", name: "Stripe", attribution_required: false }, expired_at: null, match: null,
    description_html: "<p>Build</p>", canonical_url: "https://example.com/j", apply_url: "https://example.com/apply", tags: [], duplicate_of: null, repost_of: null,
  }] as Reply,
  "GET /api/v1/jobs/j1/match": [200, { status: "PROFILE_INCOMPLETE", match: null, analysis: { method: "NONE", analyzer_version: "", analyzed_at: null, model: null, deep_status: "NONE", deep_error: null, embedded: false }, feedback: null, deep_analysis_available: false, computed_at: null, disclaimer: "" }] as Reply,
  "GET /api/v1/application-documents": [200, { data: [] }] as Reply,
  "GET /api/v1/application-documents/d1": [200, doc()] as Reply,
  "GET /api/v1/application-documents/d1/versions": [200, { data: [version()] }] as Reply,
} satisfies Record<string, Reply>;
