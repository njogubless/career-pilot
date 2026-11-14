import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { formatSalary, remoteSummary } from "../lib/format";
import { mockApi, renderApp, signedIn, type Reply } from "./render";

const job = (over: Record<string, unknown> = {}) => ({
  id: "j1",
  title: "Backend Engineer",
  company: "Acme",
  department: "Engineering",
  location: "Remote - US",
  remote_status: "REMOTE",
  remote_scope: "COUNTRIES",
  remote_countries: ["US"],
  remote_region: "",
  location_countries: ["US"],
  employment_type: "FULL_TIME",
  salary: null,
  salary_text: "",
  posted_at: new Date(Date.now() - 3 * 3600_000).toISOString(),
  first_seen_at: new Date().toISOString(),
  source: { kind: "GREENHOUSE", name: "Acme", attribution_required: false },
  expired_at: null,
  ...over,
});

const discoverApi = {
  ...signedIn,
  "GET /api/v1/job-sources": [200, { data: [{ id: "s1", kind: "GREENHOUSE", identifier: "acme", display_name: "Acme", enabled: true, last_success_at: new Date().toISOString(), last_run_at: null, last_error: null, consecutive_failures: 0, active_jobs: 2, health: "HEALTHY", attribution_required: false }] }] as Reply,
  "GET /api/v1/discovery/runs": [200, { data: [] }] as Reply,
  "GET /api/v1/saved-searches": [200, { data: [] }] as Reply,
} satisfies Record<string, Reply>;

describe("discover", () => {
  it("lists jobs honestly: undisclosed salary and stated remote scope", async () => {
    mockApi({
      ...discoverApi,
      "GET /api/v1/jobs": [200, { data: [job(), job({ id: "j2", title: "Designer", salary: { min: 4000, max: 6000, currency: "USD", period: "MONTH" }, remote_scope: "WORLDWIDE", remote_countries: [] })], next_cursor: null, total: 2 }],
    });
    renderApp("/discover");
    const card = (await screen.findByRole("link", { name: "Backend Engineer" })).closest("article")!;
    expect(within(card).getByText("Salary not disclosed")).toBeInTheDocument();
    expect(within(card).getByText("Remote · US only")).toBeInTheDocument();
    expect(screen.getByText("2 jobs")).toBeInTheDocument();
    expect(screen.getByText(/\[Salary: disclosed\]/)).toBeInTheDocument();
    expect(screen.getAllByText("Match · Phase 4")).toHaveLength(2);
    expect(await screen.findByText("1/1 healthy")).toBeInTheDocument();
  });

  it("sends filters from the URL and updates them from the controls", async () => {
    const calls = mockApi({ ...discoverApi, "GET /api/v1/jobs": [200, { data: [], next_cursor: null, total: 0 }] });
    const u = userEvent.setup();
    renderApp("/discover?remote=REMOTE");
    expect(await screen.findByText("No jobs match these filters")).toBeInTheDocument();
    expect(calls.some((c) => c.url.startsWith("/api/v1/jobs?") && c.url.includes("remote=REMOTE"))).toBe(true);

    await u.type(screen.getByLabelText("Search jobs"), "golang{Enter}");
    await waitFor(() => expect(calls.some((c) => c.url.includes("q=golang") && c.url.includes("remote=REMOTE"))).toBe(true));
    await u.selectOptions(screen.getByLabelText("Source"), "ASHBY");
    await waitFor(() => expect(calls.some((c) => c.url.includes("source=ASHBY"))).toBe(true));
  });

  it("shows source attribution with a followed link when the source requires it", async () => {
    mockApi({
      ...signedIn,
      "GET /api/v1/jobs/j9": [200, {
        ...job({ id: "j9", source: { kind: "REMOTEOK", name: "Remote OK", attribution_required: true } }),
        description_html: "<p>Build things</p>", canonical_url: "https://remoteok.com/remote-jobs/x", apply_url: "https://remoteok.com/apply/x",
        tags: ["go"], duplicate_of: null, repost_of: null, last_seen_at: new Date().toISOString(),
      }],
    });
    renderApp("/jobs/j9");
    const link = await screen.findByRole("link", { name: /remote ok \(original listing\)/i });
    expect(link).toHaveAttribute("href", "https://remoteok.com/remote-jobs/x");
    expect(link.getAttribute("rel")).not.toContain("nofollow");
    expect(screen.getByRole("link", { name: /apply on the official site/i })).toHaveAttribute("rel", "noopener noreferrer");
    expect(screen.getByText(/does not estimate salaries/i)).toBeInTheDocument();
    expect(screen.getByText("Build things")).toBeInTheDocument();
  });
});

describe("format", () => {
  it("formats salaries without conversion", () => {
    expect(formatSalary({ min: 4000, max: 6000, currency: "USD", period: "MONTH" })).toMatch(/4,000.*6,000 \/ mo/);
  });
  it("states unknown remote scope plainly", () => {
    expect(remoteSummary({ remote_status: "REMOTE", remote_scope: "UNKNOWN", remote_countries: [], remote_region: "" })).toBe("Remote · applicant location not stated");
    expect(remoteSummary({ remote_status: "UNKNOWN", remote_scope: "NOT_REMOTE", remote_countries: [], remote_region: "" })).toBe("Work arrangement not stated");
  });
});
