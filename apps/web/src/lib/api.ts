/**
 * Minimal typed client for the CareerPilot API (/api/v1).
 * Errors are normalised to ApiError using the server's error envelope.
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

function isEnvelope(v: unknown): v is ApiErrorBody {
  return typeof v === "object" && v !== null && "error" in v && typeof (v as ApiErrorBody).error?.code === "string";
}

export async function apiFetch<T>(path: string, init: RequestInit = {}): Promise<T> {
  let res: Response;
  try {
    res = await fetch(path, {
      ...init,
      credentials: "same-origin",
      headers: { Accept: "application/json", ...(init.body ? { "Content-Type": "application/json" } : {}), ...init.headers },
    });
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
