import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router";
import { vi } from "vitest";
import { setCsrfToken } from "../lib/api";
import { routes } from "../routes/router";

export function renderApp(path = "/") {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createMemoryRouter(routes, { initialEntries: [path] });
  const utils = render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return { ...utils, router };
}

type Reply = [status: number, body?: unknown];
type Handler = Reply | ((init: RequestInit, url: string) => Reply);
export type Call = { method: string; url: string; headers: Headers; body: unknown };

/**
 * Route-aware fetch mock keyed by "METHOD /path" (query strings ignored).
 * Unmatched requests fail loudly so tests never silently pass on wrong URLs.
 */
export function mockApi(handlers: Record<string, Handler>) {
  setCsrfToken(null);
  const calls: Call[] = [];
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init = {}) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    const method = (init.method ?? "GET").toUpperCase();
    const path = url.split("?")[0];
    const body = typeof init.body === "string" ? JSON.parse(init.body) : init.body;
    calls.push({ method, url, headers: new Headers(init.headers), body });
    const h = handlers[`${method} ${path}`];
    if (!h) return new Response(JSON.stringify({ error: { code: "NOT_FOUND", message: `unmocked ${method} ${path}` } }), { status: 404 });
    const [status, payload] = typeof h === "function" ? h(init, url) : h;
    return status === 204 ? new Response(null, { status }) : new Response(JSON.stringify(payload ?? {}), { status, headers: { "Content-Type": "application/json" } });
  });
  return calls;
}

export const user = { id: "u1", email: "amani@example.com", display_name: "Amani Otieno", created_at: "2026-11-14T00:00:00Z" };

export const meta = {
  version: "test",
  environment: "test",
  integrations: {
    ai_service: { status: "available" },
    llm: { status: "not_configured", detail: "No LLM provider credentials configured." },
    document_storage: { status: "available" },
    gmail: { status: "not_implemented", detail: "Planned for Phase 7." },
  },
};

export const emptyProfile = {
  basics: { headline: "", summary: "", location_city: "", location_country: null, years_experience: null, links: [] },
  experiences: [],
  education: [],
  skills: [],
  certifications: [],
  projects: [],
  preferences: {
    target_roles: [],
    industries: [],
    employment_types: [],
    preferred_locations: [],
    remote_mode: "REMOTE_ONLY",
    work_eligible_countries: [],
    min_salary: null,
    salary_currency: null,
    salary_period: null,
    needs_sponsorship: null,
    willing_to_relocate: null,
    notice_period_days: null,
    seniority_levels: [],
  },
  completeness: {
    score: 10,
    unverified_facts: 0,
    missing: [{ key: "headline", label: "Add a professional headline", impact: "x", weight: 10 }],
  },
};

export const signedIn = {
  "GET /api/v1/auth/session": [200, { user, csrf_token: "csrf-123" }] as Reply,
  "GET /api/v1/meta": [200, meta] as Reply,
  "GET /api/v1/profiles/me": [200, emptyProfile] as Reply,
};
