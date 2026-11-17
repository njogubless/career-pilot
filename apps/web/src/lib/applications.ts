import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { TagTone } from "../components/ui/Tag";
import { api, apiFetch } from "./api";

/** Application pipeline (Phase 6): state machine, approval, assisted submission, evidence. */

export type AppStatus =
  | "DISCOVERED"
  | "SHORTLISTED"
  | "PREPARING"
  | "AWAITING_APPROVAL"
  | "APPROVED"
  | "FAILED"
  | "SUBMITTED"
  | "ACKNOWLEDGED"
  | "ASSESSMENT"
  | "INTERVIEWING"
  | "OFFER"
  | "REJECTED"
  | "WITHDRAWN";

export type Channel = "ASSISTED" | "EMAIL";

export type AppSummary = {
  id: string;
  status: AppStatus;
  channel: Channel | null;
  job: { id: string; title: string; company: string; location: string; apply_url: string; expired_at: string | null };
  match: { score: number; eligibility: string } | null;
  approved_documents: number;
  pending_attempt: boolean;
  approved_at: string | null;
  submitted_at: string | null;
  created_at: string;
  updated_at: string;
  status_changed_at: string;
};

export type PackageDoc = {
  document_id: string;
  kind: "CV" | "COVER_LETTER" | "APPLICATION_ANSWERS";
  approval_state: "NONE" | "CURRENT" | "OUTDATED";
  approved_version_id: string | null;
  approved_version_no: number | null;
  current_version_no: number | null;
  approved_at: string | null;
};

export type Attempt = {
  id: string;
  channel: Channel;
  actor: "USER" | "AGENT" | "SYSTEM";
  status: "PENDING" | "SUCCEEDED" | "FAILED" | "CANCELLED";
  idempotency_key: string;
  failure_reason: string | null;
  error_message: string | null;
  started_at: string;
  finished_at: string | null;
};

export type Evidence = {
  id: string;
  attempt_id: string;
  type: "USER_ATTESTED" | "EMAIL_SENT" | "EMAIL_CONFIRMATION" | "ATS_CONFIRMATION";
  reference: string;
  note: string;
  document_id: string | null;
  occurred_at: string;
  created_at: string;
};

export type HistoryEntry = { from_status: AppStatus | null; to_status: AppStatus; actor: string; reason: string; created_at: string };

export type AutomationSettings = {
  mode: "DISCOVERY_ONLY" | "HUMAN_APPROVED" | "CONTROLLED";
  paused: boolean;
  paused_at: string | null;
  daily_submission_limit: number;
  updated_at: string | null;
};

export type AppDetail = AppSummary & {
  notes: string;
  pinned: { cv_version_id: string | null; cover_letter_version_id: string | null; answers_version_id: string | null };
  package: { documents: PackageDoc[]; ready: boolean; blockers: string[]; warnings: string[] };
  allowed_transitions: AppStatus[];
  channels: { channel: Channel; available: boolean; detail?: string }[];
  history: HistoryEntry[];
  attempts: Attempt[];
  evidence: Evidence[];
  automation: AutomationSettings;
};

export type AppStats = {
  by_status: Record<AppStatus, number>;
  active: number;
  awaiting_approval: number;
  interviewing: number;
  offers: number;
  submitted: number;
  response_rate: number | null;
  median_days_to_response: number | null;
  automation_paused: boolean;
};

export type Activity = {
  application_id: string;
  job_title: string;
  company: string;
  from_status: AppStatus | null;
  to_status: AppStatus;
  actor: string;
  reason: string;
  created_at: string;
};

export const statusLabel: Record<AppStatus, string> = {
  DISCOVERED: "Discovered",
  SHORTLISTED: "Shortlisted",
  PREPARING: "Preparing",
  AWAITING_APPROVAL: "Awaiting approval",
  APPROVED: "Approved",
  FAILED: "Submission failed",
  SUBMITTED: "Submitted",
  ACKNOWLEDGED: "Acknowledged",
  ASSESSMENT: "Assessment",
  INTERVIEWING: "Interviewing",
  OFFER: "Offer",
  REJECTED: "Rejected",
  WITHDRAWN: "Withdrawn",
};

export const statusTone: Record<AppStatus, TagTone> = {
  DISCOVERED: "outline",
  SHORTLISTED: "outline",
  PREPARING: "stone",
  AWAITING_APPROVAL: "citron",
  APPROVED: "graphite",
  FAILED: "terracotta",
  SUBMITTED: "olive",
  ACKNOWLEDGED: "olive",
  ASSESSMENT: "olive",
  INTERVIEWING: "olive",
  OFFER: "citron",
  REJECTED: "outline",
  WITHDRAWN: "outline",
};

/** Pipeline columns (Stitch Kanban), each grouping statuses. */
export const columns: { key: string; title: string; statuses: AppStatus[] }[] = [
  { key: "shortlist", title: "Shortlisted", statuses: ["DISCOVERED", "SHORTLISTED"] },
  { key: "prep", title: "Preparing", statuses: ["PREPARING"] },
  { key: "gate", title: "Approval gate", statuses: ["AWAITING_APPROVAL", "APPROVED", "FAILED"] },
  { key: "submitted", title: "Submitted", statuses: ["SUBMITTED", "ACKNOWLEDGED"] },
  { key: "interviews", title: "Interviews & offers", statuses: ["ASSESSMENT", "INTERVIEWING", "OFFER"] },
  { key: "closed", title: "Closed", statuses: ["REJECTED", "WITHDRAWN"] },
];

/** What the user should do next, by status. */
export function nextStep(a: Pick<AppSummary, "status" | "pending_attempt" | "approved_documents">): string {
  if (a.pending_attempt) return "Submission in progress: confirm or report the outcome";
  switch (a.status) {
    case "DISCOVERED":
    case "SHORTLISTED":
      return "Start preparing documents";
    case "PREPARING":
      return a.approved_documents > 0 ? "Request approval" : "Approve a tailored CV";
    case "AWAITING_APPROVAL":
      return "Review and approve the package";
    case "APPROVED":
      return "Submit on the official site";
    case "FAILED":
      return "Retry, reopen or withdraw";
    case "SUBMITTED":
    case "ACKNOWLEDGED":
      return "Waiting for the employer";
    case "ASSESSMENT":
    case "INTERVIEWING":
      return "Record the outcome when you hear back";
    case "OFFER":
      return "Decide on the offer";
    default:
      return "Closed";
  }
}

const base = "/api/v1/applications";

export function useApplications() {
  return useQuery({ queryKey: ["applications"], queryFn: () => api.get<{ data: AppSummary[] }>(base) });
}

export function useApplicationStats() {
  return useQuery({ queryKey: ["applications", "stats"], queryFn: () => api.get<AppStats>(`${base}/stats`) });
}

export function useApplicationActivity(limit = 20) {
  return useQuery({ queryKey: ["applications", "activity", limit], queryFn: () => api.get<{ data: Activity[] }>(`${base}/activity?limit=${limit}`) });
}

export function useApplication(id: string) {
  return useQuery({ queryKey: ["application", id], queryFn: () => api.get<AppDetail>(`${base}/${id}`) });
}

export function useJobApplication(jobId: string) {
  return useQuery({ queryKey: ["job-application", jobId], queryFn: () => api.get<{ application: AppSummary | null }>(`/api/v1/jobs/${jobId}/application`) });
}

function useRefresh() {
  const qc = useQueryClient();
  return (detail?: AppDetail) => {
    if (detail) qc.setQueryData(["application", detail.id], detail);
    void qc.invalidateQueries({ queryKey: ["applications"] });
    void qc.invalidateQueries({ queryKey: ["job-application"] });
  };
}

export function useCreateApplication() {
  const refresh = useRefresh();
  return useMutation({ mutationFn: (job_id: string) => api.post<AppDetail>(base, { job_id }), onSuccess: refresh });
}

/** One hook per action; each returns the fresh application detail. */
export function useApplicationActions(id: string) {
  const refresh = useRefresh();
  const m = <V,>(fn: (v: V) => Promise<AppDetail>) => ({ mutationFn: fn, onSuccess: (d: AppDetail) => refresh(d) });
  return {
    transition: useMutation(m((v: { to: AppStatus; reason?: string }) => api.post<AppDetail>(`${base}/${id}/transitions`, v))),
    approve: useMutation(m((v: { channel: Channel; confirm: boolean }) => api.post<AppDetail>(`${base}/${id}/approval`, v))),
    revoke: useMutation(m(() => api.del<AppDetail>(`${base}/${id}/approval`))),
    notes: useMutation(m((notes: string) => api.put<AppDetail>(`${base}/${id}/notes`, { notes }))),
    confirm: useMutation(
      m((v: { attemptId: string; body: { attest: boolean; reference: string; note: string; document_id?: string } }) =>
        api.post<AppDetail>(`${base}/${id}/submissions/${v.attemptId}/confirm`, v.body),
      ),
    ),
    fail: useMutation(m((v: { attemptId: string; reason: string; message: string }) => api.post<AppDetail>(`${base}/${id}/submissions/${v.attemptId}/fail`, { reason: v.reason, message: v.message }))),
    cancel: useMutation(m((attemptId: string) => api.post<AppDetail>(`${base}/${id}/submissions/${attemptId}/cancel`))),
  };
}

export function useStartSubmission(id: string) {
  const qc = useQueryClient();
  return useMutation({
    // The caller generates the key once per user action, so a retry replays it.
    mutationFn: (key: string) =>
      apiFetch<{ attempt: Attempt; replayed: boolean }>(`${base}/${id}/submissions`, { method: "POST", headers: { "Idempotency-Key": key } }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["application", id] });
      void qc.invalidateQueries({ queryKey: ["applications"] });
    },
  });
}

export function useDeleteApplication(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.del(`${base}/${id}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["applications"] });
      void qc.invalidateQueries({ queryKey: ["job-application"] });
    },
  });
}

export function useAutomation() {
  return useQuery({ queryKey: ["automation"], queryFn: () => api.get<AutomationSettings>("/api/v1/automation/settings") });
}

export function useAutomationActions() {
  const qc = useQueryClient();
  const done = (s: AutomationSettings) => {
    qc.setQueryData(["automation"], s);
    void qc.invalidateQueries({ queryKey: ["applications", "stats"] });
  };
  return {
    update: useMutation({
      mutationFn: (v: { mode: AutomationSettings["mode"]; daily_submission_limit: number }) => api.put<AutomationSettings>("/api/v1/automation/settings", v),
      onSuccess: done,
    }),
    pause: useMutation({ mutationFn: () => api.post<AutomationSettings>("/api/v1/automation/pause"), onSuccess: done }),
    resume: useMutation({ mutationFn: () => api.post<AutomationSettings>("/api/v1/automation/resume"), onSuccess: done }),
  };
}

export function newIdempotencyKey() {
  return `web-${crypto.randomUUID()}`;
}
