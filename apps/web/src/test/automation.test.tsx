import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { mockApi, renderApp, signedIn } from "./render";

const now = new Date().toISOString();
const rate = (n: number, d: number) => ({ numerator: n, denominator: d, value: d ? n / d : null, low_sample: d < 5 });
const weeks = Array.from({ length: 12 }, (_, i) => new Date(Date.UTC(2026, 8, 7 + i * 7)).toISOString());

const overview = {
  period_days: 90,
  since: now,
  funnel: {
    jobs_discovered: 1200,
    jobs_matched: 40,
    stages: [
      { key: "SHORTLISTED", label: "Shortlisted", count: 6, from_previous: null },
      { key: "PREPARING", label: "Preparing", count: 4, from_previous: rate(4, 6) },
      { key: "APPROVED", label: "Approved", count: 3, from_previous: rate(3, 4) },
      { key: "SUBMITTED", label: "Submitted", count: 2, from_previous: rate(2, 3) },
      { key: "RESPONDED", label: "Employer responded", count: 1, from_previous: rate(1, 2) },
      { key: "INTERVIEW", label: "Interview", count: 1, from_previous: rate(1, 1) },
      { key: "OFFER", label: "Offer", count: 0, from_previous: rate(0, 1) },
    ],
    rejected: 1,
    withdrawn: 0,
  },
  durations: [{ key: "shortlist_to_submit", label: "Shortlist to submission", median_days: 2.5, samples: 2 }],
  outreach: { introductions_sent: 4, follow_ups_sent: 1, introductions_replied: 1, bounced: 1, reply_rate: rate(1, 3) },
  sources: [{ source: "Remotive", kind: "REMOTIVE", jobs_matched: 30, applications: 5, submitted: 2, responses: 1, interviews: 1, response_rate: rate(1, 2) }],
  skill_weeks: weeks.slice(4),
  skill_trends: [{ skill: "Kubernetes", total: 9, share: [0, 0.2, 0.5, 0, 1, 0.25, 0, 0.4] }],
  weekly: weeks.map((start, i) => ({ start, applications: i % 3, submitted: i % 2, responses: 0, outreach_sent: 0, strong_matches: i })),
  computed_at: now,
};

const report = {
  id: "r1",
  period_start: "2026-11-09",
  period_end: "2026-11-15",
  trigger: "MANUAL",
  created_at: now,
  data: {
    strong_matches: 3, shortlisted: 2, submitted: 1, responses: 0, interviews: 0, offers: 0, rejections: 0, outreach_sent: 0, outreach_replies: 0,
    top_skill_gaps: [{ skill: "Kubernetes", total: 2 }],
    highlights: ["You submitted 1 application with evidence."],
    recommendations: ["Kubernetes is missing from 2 strong matches; add it to your profile if you have verifiable experience."],
  },
};

describe("career analytics", () => {
  it("shows funnel, low-sample rates and table views from the API, and generates a report", async () => {
    let reports: unknown[] = [];
    const calls = mockApi({
      ...signedIn,
      "GET /api/v1/analytics/overview": [200, overview],
      "GET /api/v1/analytics/reports": () => [200, { data: reports }],
      "POST /api/v1/analytics/reports": () => {
        reports = [report];
        return [201, report];
      },
    });
    const u = userEvent.setup();
    renderApp("/analytics");
    expect(await screen.findByRole("heading", { level: 1, name: "Career Analytics" })).toBeInTheDocument();
    expect(await screen.findByText("of 1,200 jobs discovered")).toBeInTheDocument();
    expect(screen.getByText("50% of previous · small sample")).toBeInTheDocument();
    expect(screen.getByText("2.5 days")).toBeInTheDocument();
    expect(screen.getByText("Remotive")).toBeInTheDocument();
    expect(screen.getByText(/Kubernetes/)).toBeInTheDocument();

    // Every chart has a table view.
    const funnel = screen.getByRole("heading", { name: "Where your applications got to" }).closest("section")!;
    await u.click(within(funnel).getByRole("button", { name: "Show as table" }));
    expect(within(funnel).getByRole("cell", { name: "67% (2/3)" })).toBeInTheDocument();

    await u.click(screen.getByRole("button", { name: "30 days" }));
    await waitFor(() => expect(calls.some((c) => c.url.endsWith("period=30"))).toBe(true));

    expect(screen.getByText(/No reports yet/)).toBeInTheDocument();
    await u.click(screen.getByRole("button", { name: "Report on the last 7 days" }));
    expect(await screen.findByText("You submitted 1 application with evidence.")).toBeInTheDocument();
  });
});

describe("notifications", () => {
  it("lists notifications and marks them read", async () => {
    let unread = true;
    const calls = mockApi({
      ...signedIn,
      "GET /api/v1/notifications": () => [
        200,
        { data: [{ id: "n1", kind: "APPROVAL_REQUESTED", title: "Approval needed", body: "1 package", link: "/intelligence/workflows/w1", read_at: unread ? null : now, created_at: now }], unread_count: unread ? 1 : 0 },
      ],
      "POST /api/v1/notifications/n1/read": () => {
        unread = false;
        return [204];
      },
    });
    const u = userEvent.setup();
    renderApp("/notifications");
    expect(await screen.findByRole("link", { name: "Approval needed" })).toHaveAttribute("href", "/intelligence/workflows/w1");
    expect(screen.getByText(/1 unread/)).toBeInTheDocument();
    await u.click(screen.getByRole("button", { name: 'Mark "Approval needed" as read' }));
    expect(await screen.findByText(/All caught up/)).toBeInTheDocument();
    expect(calls.find((c) => c.method === "POST")?.headers.get("X-CSRF-Token")).toBe("csrf-123");
  });

  it("saves notification preferences in settings", async () => {
    const prefs = { high_match: true, min_match_score: 80, approvals: true, workflow_events: true, replies: true, follow_ups: true, weekly_report: true };
    const calls = mockApi({
      ...signedIn,
      "GET /api/v1/notifications/preferences": [200, prefs],
      "PUT /api/v1/notifications/preferences": (init) => [200, JSON.parse(String(init.body))],
    });
    const u = userEvent.setup();
    renderApp("/settings");
    const box = await screen.findByLabelText(/Weekly career report/);
    await u.click(box);
    const score = screen.getByLabelText("Minimum match score for a notification");
    await u.clear(score);
    await u.type(score, "90");
    await u.click(screen.getByRole("button", { name: "Save notification preferences" }));
    await waitFor(() => expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ ...prefs, weekly_report: false, min_match_score: 90 }));
  });
});

describe("workflow schedules", () => {
  const schedule = {
    id: "s1", name: "Monday search", objective: "Find backend jobs", template: "DISCOVER_AND_RANK",
    params: { query: "backend", remote_only: true, country: "", min_monthly_salary: null, require_disclosed_salary: false, posted_within_days: 30, count: 5, min_score: 50, use_ai: false },
    cadence: "WEEKLY", weekday: 1, hour: 8, timezone: "Africa/Nairobi", enabled: true, next_run_at: now,
    last_run_at: now, last_outcome: "SKIPPED_PAUSED", last_message: "Automation is paused.", created_at: now,
  };

  it("lists schedules with their last outcome, disables and creates one from the plan", async () => {
    let list = [schedule];
    const calls = mockApi({
      ...signedIn,
      "GET /api/v1/workflows": [200, { data: [] }],
      "GET /api/v1/approvals": [200, { data: [] }],
      "GET /api/v1/workflows/templates": [200, { plan_version: "plan-v1", data: [] }],
      "GET /api/v1/workflow-schedules": () => [200, { data: list }],
      "PUT /api/v1/workflow-schedules/s1": (init) => {
        list = [{ ...schedule, ...JSON.parse(String(init.body)) }];
        return [200, list[0]];
      },
      "POST /api/v1/workflow-schedules": (init) => [201, { ...schedule, id: "s2", ...JSON.parse(String(init.body)) }],
    });
    const u = userEvent.setup();
    renderApp("/intelligence");
    expect(await screen.findByText("Monday search")).toBeInTheDocument();
    expect(screen.getByText("Skipped: automation paused")).toBeInTheDocument();
    expect(screen.getByText(/Every Monday at 08:00 Africa\/Nairobi/)).toBeInTheDocument();

    await u.click(screen.getByRole("button", { name: "Disable" }));
    await waitFor(() => expect(calls.find((c) => c.method === "PUT")?.body).toMatchObject({ enabled: false, name: "Monday search" }));
    expect(await screen.findByRole("button", { name: "Enable" })).toBeInTheDocument();

    await u.type(screen.getByLabelText("Your objective"), "Find remote Go jobs");
    await u.click(screen.getByRole("button", { name: "Repeat this plan on a schedule" }));
    await u.type(screen.getByLabelText("Schedule name"), "Daily Go");
    await u.selectOptions(screen.getByLabelText("How often"), "DAILY");
    await u.click(screen.getByRole("button", { name: "Save schedule" }));
    await waitFor(() =>
      expect(calls.find((c) => c.method === "POST")?.body).toMatchObject({ name: "Daily Go", objective: "Find remote Go jobs", cadence: "DAILY", weekday: null, enabled: true }),
    );
    expect(await screen.findByText(/Scheduled\. Next run/)).toBeInTheDocument();
  });
});

describe("data rights", () => {
  it("downloads an export and deletes the account after confirmation", async () => {
    let deleted = false;
    const calls = mockApi({
      ...signedIn,
      "POST /api/v1/users/me/export": (init) =>
        JSON.parse(String(init.body)).password === "right"
          ? [200, {}]
          : [422, { error: { code: "VALIDATION_FAILED", message: "x", details: { password: "The password is incorrect." } } }],
      "GET /api/v1/auth/session": () => (deleted ? [401, { error: { code: "UNAUTHENTICATED", message: "x" } }] : signedIn["GET /api/v1/auth/session"]),
      "POST /api/v1/users/me/delete": () => {
        deleted = true;
        return [204];
      },
    });
    const created: string[] = [];
    URL.createObjectURL = (() => {
      created.push("blob");
      return "blob:x";
    }) as typeof URL.createObjectURL;
    URL.revokeObjectURL = () => {};
    const u = userEvent.setup();
    const { router } = renderApp("/settings");

    const exportPw = await screen.findByLabelText("Confirm your password");
    await u.type(exportPw, "wrong");
    await u.click(screen.getByRole("button", { name: "Download my data" }));
    expect(await screen.findByText("The password is incorrect.")).toBeInTheDocument();
    await u.clear(exportPw);
    await u.type(exportPw, "right");
    await u.click(screen.getByRole("button", { name: "Download my data" }));
    expect(await screen.findByText("Your export has downloaded.")).toBeInTheDocument();
    expect(created).toHaveLength(1);
    expect(calls.filter((c) => c.url.endsWith("/users/me/export")).at(-1)?.headers.get("X-CSRF-Token")).toBe("csrf-123");

    const del = screen.getByRole("button", { name: "Delete my account permanently" });
    await u.type(screen.getByLabelText("Your password"), "right");
    expect(del).toBeDisabled();
    await u.type(screen.getByLabelText("Type DELETE to confirm"), "DELETE");
    await u.click(del);
    expect(await screen.findByText("Your account and its data have been deleted.")).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/login");
    expect(calls.find((c) => c.url.endsWith("/users/me/delete"))?.body).toEqual({ password: "right", confirm: "DELETE" });
  });
});
