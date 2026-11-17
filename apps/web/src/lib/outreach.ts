import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { TagTone } from "../components/ui/Tag";
import { api, apiFetch } from "./api";
import type { Validation } from "./preparation";

/** Recruiter outreach (Phase 7): contacts, Gmail, approved emails, replies, follow-ups. */

export type Verification = "UNVERIFIED" | "DOMAIN_OK" | "INVALID" | "BOUNCED";

export type Contact = {
  id: string;
  name: string;
  title: string;
  company: string;
  job_id: string | null;
  profile_url: string;
  email: string | null;
  source: "USER_PROVIDED" | "JOB_POSTING";
  source_evidence: string;
  email_verification: Verification;
  verified_at: string | null;
  confidence: "HIGH" | "MEDIUM" | "LOW";
  do_not_contact: boolean;
  do_not_contact_reason: string;
  notes: string;
  discovered_at: string;
  updated_at: string;
  messages_sent: number;
  last_contacted_at: string | null;
};

export type ContactInput = Pick<Contact, "name" | "title" | "company" | "profile_url" | "notes" | "do_not_contact" | "do_not_contact_reason"> & {
  email: string;
  job_id: string | null;
};

export type MessageStatus = "DRAFT" | "APPROVED" | "SENDING" | "SENT" | "FAILED" | "UNKNOWN" | "CANCELLED";

export type Message = {
  id: string;
  contact_id: string;
  contact_name: string;
  job_id: string | null;
  application_id: string | null;
  parent_id: string | null;
  kind: "INTRODUCTION" | "FOLLOW_UP";
  status: MessageStatus;
  origin: "AI" | "TEMPLATE" | "USER" | "FOLLOW_UP_SCHEDULER";
  to_email: string;
  subject: string;
  body: string;
  attachment_version_id: string | null;
  validation: Validation | null;
  approved_at: string | null;
  gmail_thread_id: string | null;
  sent_at: string | null;
  error_message: string | null;
  replied_at: string | null;
  prompt_version: string | null;
  created_at: string;
  updated_at: string;
  replies?: { id: string; from_address: string; is_bounce: boolean; received_at: string; application_id: string | null }[];
  follow_ups?: { sequence: number; due_at: string; status: "SCHEDULED" | "DRAFTED" | "CANCELLED"; draft_id: string | null; cancelled_reason: string | null }[];
  questions?: string[];
  warnings?: string[];
};

export type OutreachSettings = {
  daily_send_limit: number;
  recipient_cooldown_days: number;
  follow_ups_enabled: boolean;
  follow_up_after_days: number;
  max_follow_ups: number;
  signature: string;
};

export type GmailStatus = {
  configured: boolean;
  account: { email: string; scopes: string[]; status: "CONNECTED" | "NEEDS_RECONNECT"; last_error: string | null; connected_at: string } | null;
  scopes_requested: string[];
};

export const verificationLabel: Record<Verification, string> = {
  UNVERIFIED: "Not checked",
  DOMAIN_OK: "Domain accepts mail",
  INVALID: "Invalid address",
  BOUNCED: "Bounced",
};
export const verificationTone: Record<Verification, TagTone> = { UNVERIFIED: "outline", DOMAIN_OK: "olive", INVALID: "terracotta", BOUNCED: "terracotta" };

export const messageStatusLabel: Record<MessageStatus, string> = {
  DRAFT: "Draft",
  APPROVED: "Approved",
  SENDING: "Sending",
  SENT: "Sent",
  FAILED: "Failed",
  UNKNOWN: "Outcome unknown",
  CANCELLED: "Cancelled",
};
export const messageStatusTone: Record<MessageStatus, TagTone> = {
  DRAFT: "stone",
  APPROVED: "citron",
  SENDING: "outline",
  SENT: "olive",
  FAILED: "terracotta",
  UNKNOWN: "terracotta",
  CANCELLED: "outline",
};

export function useGmailStatus() {
  return useQuery({ queryKey: ["gmail"], queryFn: () => api.get<GmailStatus>("/api/v1/integrations/gmail") });
}

export function useGmailActions() {
  const qc = useQueryClient();
  return {
    connect: useMutation({
      mutationFn: () => api.post<{ auth_url: string }>("/api/v1/integrations/gmail/connect"),
      // Full-page navigation to Google's consent screen.
      onSuccess: (r) => window.location.assign(r.auth_url),
    }),
    disconnect: useMutation({ mutationFn: () => api.del("/api/v1/integrations/gmail"), onSuccess: () => void qc.invalidateQueries({ queryKey: ["gmail"] }) }),
  };
}

export function useContacts(jobId?: string) {
  return useQuery({
    queryKey: ["contacts", jobId ?? "all"],
    queryFn: () => api.get<{ data: Contact[] }>(jobId ? `/api/v1/contacts?job_id=${jobId}` : "/api/v1/contacts"),
  });
}

export function useContactActions() {
  const qc = useQueryClient();
  const done = () => void qc.invalidateQueries({ queryKey: ["contacts"] });
  return {
    create: useMutation({ mutationFn: (v: ContactInput) => api.post<Contact>("/api/v1/contacts", v), onSuccess: done }),
    update: useMutation({ mutationFn: (v: { id: string; body: ContactInput }) => api.put<Contact>(`/api/v1/contacts/${v.id}`, v.body), onSuccess: done }),
    remove: useMutation({ mutationFn: (id: string) => api.del(`/api/v1/contacts/${id}`), onSuccess: done }),
    verify: useMutation({ mutationFn: (id: string) => api.post<Contact>(`/api/v1/contacts/${id}/verify`), onSuccess: done }),
    discover: useMutation({
      mutationFn: (jobId: string) => api.post<{ data: { contact: Contact; created: boolean }[] }>(`/api/v1/jobs/${jobId}/contacts/discover`),
      onSuccess: done,
    }),
  };
}

export function useMessages(filter: { status?: string; application_id?: string; contact_id?: string } = {}) {
  const qs = new URLSearchParams(Object.entries(filter).filter(([, v]) => v) as [string, string][]).toString();
  return useQuery({
    queryKey: ["outreach-messages", qs],
    queryFn: () => api.get<{ data: Message[] }>(`/api/v1/outreach/messages${qs ? `?${qs}` : ""}`),
  });
}

export function useMessage(id: string) {
  return useQuery({ queryKey: ["outreach-message", id], queryFn: () => api.get<Message>(`/api/v1/outreach/messages/${id}`) });
}

export function useMessageActions(id: string) {
  const qc = useQueryClient();
  const done = (m: Message) => {
    qc.setQueryData(["outreach-message", id], (old: Message | undefined) => ({ ...m, questions: m.questions ?? old?.questions, warnings: m.warnings ?? old?.warnings }));
    void qc.invalidateQueries({ queryKey: ["outreach-messages"] });
    void qc.invalidateQueries({ queryKey: ["contacts"] });
  };
  const base = `/api/v1/outreach/messages/${id}`;
  return {
    edit: useMutation({ mutationFn: (v: { subject: string; body: string; attachment_version_id: string | null }) => api.put<Message>(base, v), onSuccess: done }),
    approve: useMutation({ mutationFn: (v: { confirm: boolean; acknowledge_issues: boolean }) => api.post<Message>(`${base}/approve`, v), onSuccess: done }),
    send: useMutation({
      // The caller creates the key once per click; a network retry replays it.
      mutationFn: (key: string) => apiFetch<Message>(`${base}/send`, { method: "POST", headers: { "Idempotency-Key": key } }),
      onSuccess: done,
    }),
    cancel: useMutation({ mutationFn: () => api.post<Message>(`${base}/cancel`), onSuccess: done }),
    markNotSent: useMutation({ mutationFn: () => api.post<Message>(`${base}/mark-not-sent`), onSuccess: done }),
  };
}

export function useCreateDraft() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: { contact_id: string; job_id?: string | null; mode: "AI" | "TEMPLATE"; notes?: string; attachment_version_id?: string | null }) =>
      api.post<Message>("/api/v1/outreach/messages", v),
    onSuccess: (m) => {
      qc.setQueryData(["outreach-message", m.id], m);
      void qc.invalidateQueries({ queryKey: ["outreach-messages"] });
    },
  });
}

export function usePollReplies() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.post<{ threads_checked: number; replies_found: number; bounces_found: number }>("/api/v1/outreach/poll"),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["outreach-messages"] });
      void qc.invalidateQueries({ queryKey: ["outreach-message"] });
      void qc.invalidateQueries({ queryKey: ["contacts"] });
    },
  });
}

export function useOutreachSettings() {
  return useQuery({ queryKey: ["outreach-settings"], queryFn: () => api.get<OutreachSettings>("/api/v1/outreach/settings") });
}

export function useSaveOutreachSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: OutreachSettings) => api.put<OutreachSettings>("/api/v1/outreach/settings", v),
    onSuccess: (s) => qc.setQueryData(["outreach-settings"], s),
  });
}

export function sendKey() {
  return `web-${crypto.randomUUID()}`;
}
