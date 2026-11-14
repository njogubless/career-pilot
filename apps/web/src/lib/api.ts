/**
 * Typed client for the CareerPilot API (/api/v1).
 * - Session auth uses an HttpOnly cookie (never readable from JS).
 * - State-changing requests carry the CSRF token issued with the session.
 * - Errors are normalised to ApiError from the server's error envelope.
 */

export type ApiErrorBody = {
  error: { code: string; message: string; details?: Record<string, string>; request_id?: string };
};

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly details: Record<string, string>;
  readonly requestId?: string;

  constructor(status: number, code: string, message: string, details: Record<string, string> = {}, requestId?: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.details = details;
    this.requestId = requestId;
  }
}

let csrfToken: string | null = null;

/** Called whenever the server issues a session (login, register, session fetch). */
export function setCsrfToken(token: string | null) {
  csrfToken = token;
}

function isEnvelope(v: unknown): v is ApiErrorBody {
  return typeof v === "object" && v !== null && "error" in v && typeof (v as ApiErrorBody).error?.code === "string";
}

const SAFE = new Set(["GET", "HEAD", "OPTIONS"]);

export async function apiFetch<T>(path: string, init: RequestInit = {}): Promise<T> {
  const method = (init.method ?? "GET").toUpperCase();
  const headers = new Headers(init.headers);
  headers.set("Accept", "application/json");
  if (init.body && !(init.body instanceof FormData)) headers.set("Content-Type", "application/json");
  if (!SAFE.has(method) && csrfToken) headers.set("X-CSRF-Token", csrfToken);

  let res: Response;
  try {
    res = await fetch(path, { ...init, method, headers, credentials: "same-origin" });
  } catch {
    throw new ApiError(0, "NETWORK_ERROR", "Cannot reach the CareerPilot API. Check that the server is running.");
  }

  if (res.status === 204) return undefined as T;
  const body: unknown = await res.json().catch(() => null);
  if (!res.ok) {
    if (isEnvelope(body)) {
      const e = body.error;
      throw new ApiError(res.status, e.code, e.message, e.details, e.request_id);
    }
    throw new ApiError(res.status, "UNEXPECTED_RESPONSE", `Unexpected response (${res.status}).`);
  }
  return body as T;
}

export const api = {
  get: <T>(path: string) => apiFetch<T>(path),
  post: <T>(path: string, body?: unknown) =>
    apiFetch<T>(path, { method: "POST", body: body === undefined ? undefined : JSON.stringify(body) }),
  put: <T>(path: string, body: unknown) => apiFetch<T>(path, { method: "PUT", body: JSON.stringify(body) }),
  patch: <T>(path: string, body: unknown) => apiFetch<T>(path, { method: "PATCH", body: JSON.stringify(body) }),
  del: <T = void>(path: string) => apiFetch<T>(path, { method: "DELETE" }),
  upload: <T>(path: string, form: FormData) => apiFetch<T>(path, { method: "POST", body: form }),
};

/** Field error from an ApiError's details map. */
export function apiFieldError(error: unknown, field: string): string | undefined {
  return error instanceof ApiError ? error.details[field] : undefined;
}
