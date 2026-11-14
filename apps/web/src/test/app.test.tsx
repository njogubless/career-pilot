import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { matchTone } from "../lib/score";
import { ApiError, apiFetch } from "../lib/api";
import { mockFetchJson, renderApp } from "./render";

const meta = {
  version: "test",
  environment: "test",
  integrations: {
    ai_service: { status: "available" },
    llm: { status: "not_configured", detail: "No LLM provider credentials configured." },
    gmail: { status: "not_implemented", detail: "Planned for Phase 7." },
  },
};

describe("app shell", () => {
  it("renders overview with live integration status and no sample data", async () => {
    mockFetchJson(200, meta);
    renderApp("/");
    expect(screen.getByRole("heading", { level: 1, name: /career execution, under your control/i })).toBeInTheDocument();
    const integrations = await screen.findByText("LLM provider");
    const row = integrations.closest("li")!;
    expect(within(row).getByText("Not configured")).toBeInTheDocument();
    // Metric tiles never show fabricated numbers.
    expect(screen.getAllByText("No data yet")).toHaveLength(6);
    expect(screen.queryByText(/Elena/)).not.toBeInTheDocument();
  });

  it("shows an honest error when the API is unreachable", async () => {
    vi.spyOn(globalThis, "fetch").mockRejectedValue(new TypeError("fetch failed"));
    renderApp("/");
    expect(await screen.findByText("API unreachable")).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent(/cannot reach/i);
  });

  it("disables discovery until it exists", () => {
    mockFetchJson(200, meta);
    renderApp("/");
    expect(screen.getByRole("button", { name: /run discovery cycle/i })).toBeDisabled();
  });

  it("renders roadmap modules as not available, never simulated", () => {
    mockFetchJson(200, meta);
    renderApp("/applications");
    expect(screen.getByRole("heading", { level: 1, name: "Applications" })).toBeInTheDocument();
    expect(screen.getByText("Not available yet")).toBeInTheDocument();
  });

  it("opens and closes the mobile navigation drawer with keyboard", async () => {
    mockFetchJson(200, meta);
    const user = userEvent.setup();
    renderApp("/");
    await user.click(screen.getByRole("button", { name: "Open navigation" }));
    const dialog = screen.getByRole("dialog", { name: "Navigation" });
    expect(within(dialog).getByRole("link", { name: /discover jobs/i })).toBeInTheDocument();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("renders not-found for unknown paths", () => {
    mockFetchJson(200, meta);
    renderApp("/nope");
    expect(screen.getByText("Page not found")).toBeInTheDocument();
  });
});

describe("apiFetch", () => {
  it("normalises the error envelope", async () => {
    mockFetchJson(422, { error: { code: "VALIDATION_FAILED", message: "Bad", details: { email: "required" }, request_id: "r1" } });
    const err = await apiFetch("/api/v1/x").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 422, code: "VALIDATION_FAILED", details: { email: "required" }, requestId: "r1" });
  });

  it("handles non-envelope errors", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("<html>", { status: 502 }));
    await expect(apiFetch("/api/v1/x")).rejects.toMatchObject({ code: "UNEXPECTED_RESPONSE", status: 502 });
  });
});

describe("matchTone", () => {
  it("maps scores per the design system", () => {
    expect(matchTone(95)).toBe("citron");
    expect(matchTone(90)).toBe("citron");
    expect(matchTone(75)).toBe("olive");
    expect(matchTone(69.9)).toBe("terracotta");
  });
});
