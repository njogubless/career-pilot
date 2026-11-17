import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ApiError, apiFetch } from "../lib/api";
import { safeNext } from "../lib/redirect";
import { matchTone } from "../lib/score";
import { emptyProfile, mockApi, renderApp, signedIn } from "./render";

describe("authentication", () => {
  it("redirects signed-out users to login, then back to the requested page", async () => {
    let authed = false;
    mockApi({
      ...signedIn,
      "GET /api/v1/auth/session": () => (authed ? [200, { user: { id: "u1", email: "a@b.co", display_name: "Amani Otieno", created_at: "" }, csrf_token: "c" }] : [401, { error: { code: "UNAUTHENTICATED", message: "Not signed in." } }]),
      "POST /api/v1/auth/login": () => {
        authed = true;
        return [200, { user: { id: "u1", email: "a@b.co", display_name: "Amani Otieno", created_at: "" }, csrf_token: "c" }];
      },
    });
    const u = userEvent.setup();
    const { router } = renderApp("/settings");
    expect(await screen.findByRole("heading", { name: "Welcome back." })).toBeInTheDocument();
    expect(router.state.location.search).toBe("?next=%2Fsettings");

    await u.type(screen.getByLabelText("Email"), "a@b.co");
    await u.type(screen.getByLabelText("Password"), "a-strong-passphrase");
    await u.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByRole("heading", { level: 1, name: "Settings" })).toBeInTheDocument();
  });

  it("shows the server's generic error on bad credentials", async () => {
    mockApi({
      "GET /api/v1/auth/session": [401, { error: { code: "UNAUTHENTICATED", message: "Not signed in." } }],
      "POST /api/v1/auth/login": [401, { error: { code: "UNAUTHENTICATED", message: "The email or password is incorrect." } }],
    });
    const u = userEvent.setup();
    renderApp("/login");
    await u.type(await screen.findByLabelText("Email"), "a@b.co");
    await u.type(screen.getByLabelText("Password"), "wrong");
    await u.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("The email or password is incorrect.");
  });

  it("validates registration client-side and shows server field errors", async () => {
    mockApi({
      "GET /api/v1/auth/session": [401, { error: { code: "UNAUTHENTICATED", message: "x" } }],
      "POST /api/v1/auth/register": [409, { error: { code: "CONFLICT", message: "An account with this email already exists.", details: { email: "Already registered." } } }],
    });
    const u = userEvent.setup();
    renderApp("/register");
    await u.click(await screen.findByRole("button", { name: "Create account" }));
    expect(await screen.findByText("Use at least 10 characters.")).toBeInTheDocument();

    await u.type(screen.getByLabelText("Full name"), "Amani");
    await u.type(screen.getByLabelText("Email"), "dup@example.com");
    await u.type(screen.getByLabelText("Password"), "a-strong-passphrase");
    await u.click(screen.getByRole("button", { name: "Create account" }));
    expect(await screen.findByText("Already registered.")).toBeInTheDocument();
  });

  it("only allows same-app redirects", () => {
    expect(safeNext("/profile")).toBe("/profile");
    expect(safeNext("//evil.com")).toBe("/");
    expect(safeNext("https://evil.com")).toBe("/");
    expect(safeNext("/\\evil.com")).toBe("/");
    expect(safeNext(null)).toBe("/");
  });
});

describe("app shell (signed in)", () => {
  it("greets the user and drives the checklist from real completeness data", async () => {
    mockApi(signedIn);
    renderApp("/");
    expect(await screen.findByRole("heading", { level: 1, name: /, Amani\.$/ })).toBeInTheDocument();
    expect(await screen.findByRole("link", { name: "Add a professional headline" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /upload your cv/i })).toHaveAttribute("href", "/documents");
    expect(screen.getAllByText("No data yet")).toHaveLength(5); // "Discovered" is live since Phase 3
    expect(screen.queryByText(/Elena/)).not.toBeInTheDocument();
  });

  it("queues a discovery cycle with the CSRF token and shows the result", async () => {
    const calls = mockApi({ ...signedIn, "POST /api/v1/discovery/runs": [202, { queued: true, message: "Discovery cycle queued." }] });
    const u = userEvent.setup();
    renderApp("/");
    await u.click(await screen.findByRole("button", { name: /run discovery cycle/i }));
    expect(await screen.findByText("Discovery cycle queued.")).toBeInTheDocument();
    expect(calls.find((c) => c.url === "/api/v1/discovery/runs")?.headers.get("X-CSRF-Token")).toBe("csrf-123");
  });

  it("shows live discovered-job counts", async () => {
    mockApi(signedIn);
    renderApp("/");
    expect(await screen.findByText("+5 in 24h")).toBeInTheDocument();
  });

  it("renders roadmap modules as not available, never simulated", async () => {
    mockApi(signedIn);
    renderApp("/outreach");
    expect(await screen.findByRole("heading", { level: 1, name: "Recruiter Outreach" })).toBeInTheDocument();
    expect(screen.getByText("Not available yet")).toBeInTheDocument();
  });

  it("signs out from the sidebar with the CSRF token", async () => {
    let authed = true;
    const calls = mockApi({
      ...signedIn,
      "GET /api/v1/auth/session": () => (authed ? signedIn["GET /api/v1/auth/session"] : [401, { error: { code: "UNAUTHENTICATED", message: "x" } }]),
      "POST /api/v1/auth/logout": () => {
        authed = false;
        return [204];
      },
    });
    const u = userEvent.setup();
    renderApp("/");
    const nav = await screen.findAllByRole("button", { name: "Sign out" });
    await u.click(nav[0]);
    expect(await screen.findByRole("heading", { name: "Welcome back." })).toBeInTheDocument();
    const logout = calls.find((c) => c.url === "/api/v1/auth/logout");
    expect(logout?.headers.get("X-CSRF-Token")).toBe("csrf-123");
  });

  it("opens and closes the mobile navigation drawer with keyboard", async () => {
    mockApi(signedIn);
    const u = userEvent.setup();
    renderApp("/");
    await u.click(await screen.findByRole("button", { name: "Open navigation" }));
    const dialog = screen.getByRole("dialog", { name: "Navigation" });
    expect(within(dialog).getByRole("link", { name: /discover jobs/i })).toBeInTheDocument();
    await u.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
});

describe("profile", () => {
  it("marks imported facts and lets the user confirm them", async () => {
    const profile = {
      ...emptyProfile,
      experiences: [
        {
          id: "e1", employer: "Acme", title: "Engineer", location: "", start_date: "2021-01-01", end_date: null, is_current: true,
          description: "", achievements: [], source: "CV_IMPORT", verification: "UNVERIFIED",
        },
      ],
    };
    const calls = mockApi({ ...signedIn, "GET /api/v1/profiles/me": [200, profile], "PUT /api/v1/profiles/me/experiences/e1/verification": [204] });
    const u = userEvent.setup();
    renderApp("/profile");
    expect(await screen.findByText("Needs review")).toBeInTheDocument();
    expect(screen.getByText("From CV")).toBeInTheDocument();
    await u.click(screen.getByRole("button", { name: "Confirm accurate" }));
    await waitFor(() => expect(calls.some((c) => c.url.endsWith("/experiences/e1/verification"))).toBe(true));
    const call = calls.find((c) => c.url.endsWith("/experiences/e1/verification"))!;
    expect(call.body).toEqual({ verification: "VERIFIED" });
    expect(call.headers.get("X-CSRF-Token")).toBe("csrf-123");
  });

  it("rejects non-http links before calling the API", async () => {
    const calls = mockApi(signedIn);
    const u = userEvent.setup();
    renderApp("/profile");
    await u.click(await screen.findByRole("button", { name: "Add link" }));
    await u.type(screen.getByLabelText("Label"), "Site");
    await u.type(screen.getByLabelText("URL"), "javascript:alert(1)");
    await u.click(screen.getByRole("button", { name: "Save details" }));
    expect(await screen.findByText("Enter a full http(s) URL.")).toBeInTheDocument();
    expect(calls.some((c) => c.method === "PUT")).toBe(false);
  });
});

describe("documents", () => {
  it("explains when AI analysis is unavailable and disables it", async () => {
    mockApi({
      ...signedIn,
      "GET /api/v1/documents": [200, { data: [{ id: "d1", kind: "CV", original_filename: "cv.pdf", mime_type: "application/pdf", size_bytes: 2048, created_at: "2026-11-14T00:00:00Z" }] }],
    });
    renderApp("/documents");
    expect(await screen.findByText("cv.pdf")).toBeInTheDocument();
    expect(screen.getByText(/no ai provider is configured/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /analyse cv/i })).toBeDisabled();
    expect(screen.getByRole("link", { name: /download/i })).toHaveAttribute("href", "/api/v1/documents/d1/download");
  });
});

describe("apiFetch", () => {
  it("normalises the error envelope", async () => {
    mockApi({ "GET /api/v1/x": [422, { error: { code: "VALIDATION_FAILED", message: "Bad", details: { email: "required" }, request_id: "r1" } }] });
    const err = await apiFetch("/api/v1/x").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 422, code: "VALIDATION_FAILED", details: { email: "required" }, requestId: "r1" });
  });

  it("handles network failures", async () => {
    vi.spyOn(globalThis, "fetch").mockRejectedValue(new TypeError("fetch failed"));
    await expect(apiFetch("/api/v1/x")).rejects.toMatchObject({ code: "NETWORK_ERROR" });
  });
});

describe("matchTone", () => {
  it("maps scores per the design system", () => {
    expect(matchTone(95)).toBe("citron");
    expect(matchTone(75)).toBe("olive");
    expect(matchTone(69.9)).toBe("terracotta");
  });
});
