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
  match: null,
  ...over,
});

const discoverApi = {
  ...signedIn,
  "GET /api/v1/job-sources": [200, { data: [{ id: "s1", kind: "GREENHOUSE", identifier: "acme", display_name: "Acme", enabled: true, last_success_at: new Date().toISOString(), last_run_at: null, last_error: null, consecutive_failures: 0, active_jobs: 2, health: "HEALTHY", attribution_required: false }] }] as Reply,
  "GET /api/v1/discovery/runs": [200, { data: [] }] as Reply,
  "GET /api/v1/saved-searches": [200, { data: [] }] as Reply,
  "GET /api/v1/matches/status": [200, { status: "NOT_STARTED", algorithm_version: "match-v1", candidates: 0, scored: 0, error: null, started_at: null, completed_at: null }] as Reply,
  "GET /api/v1/matches/skill-gaps": [200, { data: [], matches_considered: 0 }] as Reply,
} satisfies Record<string, Reply>;

describe("discover", () => {
  it("lists jobs honestly: undisclosed salary and stated remote scope", async () => {
    mockApi({
      ...discoverApi,
      "GET /api/v1/jobs": [200, { data: [job(), job({ id: "j2", title: "Designer", salary: { min: 4000, max: 6000, currency: "USD", period: "MONTH" }, remote_scope: "WORLDWIDE", remote_countries: [], match: { score: 81.4, eligibility: "ELIGIBLE", confidence: "MEDIUM" } })], next_cursor: null, total: 2 }],
    });
    renderApp("/discover");
    const card = (await screen.findByRole("link", { name: "Backend Engineer" })).closest("article")!;
    expect(within(card).getByText("Salary not disclosed")).toBeInTheDocument();
    expect(within(card).getByText("Remote · US only")).toBeInTheDocument();
    expect(screen.getByText("2 jobs")).toBeInTheDocument();
    expect(screen.getByText(/\[Salary: disclosed\]/)).toBeInTheDocument();
    // Unscored jobs say so; scored jobs show the rounded score, never an invented one.
    expect(within(card).getByText("Not scored")).toBeInTheDocument();
    const designer = screen.getByRole("link", { name: "Designer" }).closest("article")!;
    expect(within(designer).getByText("81")).toBeInTheDocument();
    expect(within(designer).getByText("Match")).toBeInTheDocument();
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
  it("renders the suitability decomposition with evidence, unknowns and feedback", async () => {
    const detail = { ...job({ id: "j7", title: "Senior Backend Engineer" }), description_html: "<p>Go</p>", canonical_url: "https://boards.example/j7", apply_url: "https://boards.example/j7/apply", tags: [], duplicate_of: null, repost_of: null, last_seen_at: new Date().toISOString() };
    const match = {
      algorithm_version: "match-v1", score: 72.4, eligibility: "UNKNOWN", confidence: "MEDIUM", known_weight: 85, analysis_method: "DETERMINISTIC", analyzer_version: "jd-det-1+tax-2",
      dimensions: [
        { key: "skills", label: "Skills", weight: 35, score: 66.7, summary: "2 of 3 core skills evidenced.", components: [] },
        { key: "compensation", label: "Compensation & preferences", weight: 15, score: null, summary: "Not enough information to score this dimension.", components: [] },
      ],
      checks: [{ key: "LOCATION_ELIGIBILITY", label: "Location eligibility", status: "UNKNOWN", critical: true, detail: "Add the countries where you can work to your preferences.", evidence: "Remote - US" }],
      skills: [
        { name: "Go", importance: "REQUIRED", weight: 3, status: "MATCHED", credit: 1, job_evidence: "5+ years of Go", profile_source: "SKILL_VERIFIED", profile_evidence: "Listed on your profile as Golang" },
        { name: "Kafka", importance: "REQUIRED", weight: 3, status: "MISSING", credit: 0, job_evidence: "Experience with Kafka" },
      ],
      missing_skills: ["Kafka"], missing_preferred: [], blockers: [], signals: [{ kind: "NEGATIVE", text: "No evidence on your profile for Kafka." }],
      unknowns: ["The posting does not disclose a salary."], explanation: ["Supporting evidence: Go (confirmed on your profile)."],
    };
    const calls = mockApi({
      ...signedIn,
      "GET /api/v1/jobs/j7": [200, detail],
      "GET /api/v1/jobs/j7/match": [200, { status: "READY", match, analysis: { method: "DETERMINISTIC", analyzer_version: "jd-det-1+tax-2", analyzed_at: new Date().toISOString(), model: null, deep_status: "NONE", deep_error: null, embedded: true }, feedback: null, deep_analysis_available: false, computed_at: new Date().toISOString(), disclaimer: "A suitability score is not a prediction of interviews or offers." }],
      "PUT /api/v1/jobs/j7/match/feedback": [200, { rating: "UP", reason: null, note: "", dismissed: false, updated_at: new Date().toISOString() }],
    });
    const u = userEvent.setup();
    renderApp("/jobs/j7");
    expect(await screen.findByLabelText("Suitability score 72 out of 100")).toBeInTheDocument();
    expect(screen.getByText("Eligibility unconfirmed")).toBeInTheDocument();
    expect(screen.getByText("Unknown: excluded from the score")).toBeInTheDocument();
    expect(screen.getByText("Listed on your profile as Golang")).toBeInTheDocument();
    expect(screen.getByText("Supporting evidence: Go (confirmed on your profile).")).toBeInTheDocument();
    expect(screen.getByText(/not a prediction of interviews/)).toBeInTheDocument();
    expect(screen.getByText(/no AI provider is configured/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Run deep analysis/ })).not.toBeInTheDocument();
    await u.click(screen.getByRole("button", { name: "Relevant" }));
    await waitFor(() => expect(calls.some((c) => c.method === "PUT" && c.url === "/api/v1/jobs/j7/match/feedback" && (c.body as { rating: string }).rating === "UP")).toBe(true));
  });

  it("explains why a job is not scored instead of inventing a score", async () => {
    const detail = { ...job({ id: "j8" }), description_html: "", canonical_url: "https://boards.example/j8", apply_url: "https://boards.example/j8/apply", tags: [], duplicate_of: null, repost_of: null, last_seen_at: new Date().toISOString() };
    mockApi({
      ...signedIn,
      "GET /api/v1/jobs/j8": [200, detail],
      "GET /api/v1/jobs/j8/match": [200, { status: "PROFILE_INCOMPLETE", message: "Add skills or work experience to your profile to see how this role fits.", match: null, analysis: { method: "DETERMINISTIC", analyzer_version: "x", analyzed_at: null, model: null, deep_status: "NONE", deep_error: null, embedded: false }, feedback: null, deep_analysis_available: false, computed_at: null, disclaimer: "" }],
    });
    renderApp("/jobs/j8");
    expect(await screen.findByText(/Add skills or work experience/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Complete your profile" })).toHaveAttribute("href", "/profile");
    expect(screen.queryByLabelText(/Suitability score/)).not.toBeInTheDocument();
  });

  it("formats salaries without conversion", () => {
    expect(formatSalary({ min: 4000, max: 6000, currency: "USD", period: "MONTH" })).toMatch(/4,000.*6,000 \/ mo/);
  });
  it("states unknown remote scope plainly", () => {
    expect(remoteSummary({ remote_status: "REMOTE", remote_scope: "UNKNOWN", remote_countries: [], remote_region: "" })).toBe("Remote · applicant location not stated");
    expect(remoteSummary({ remote_status: "UNKNOWN", remote_scope: "NOT_REMOTE", remote_countries: [], remote_region: "" })).toBe("Work arrangement not stated");
  });
});
