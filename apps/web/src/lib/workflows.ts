import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { TagTone } from "../components/ui/Tag";
import { api } from "./api";

/** Agent orchestration (Phase 8): objectives → validated plans → agents → approvals → reports. */

export type TemplateName = "DISCOVER_AND_RANK" | "DISCOVER_PREPARE_APPROVE";

export type Params = {
  query: string;
  remote_only: boolean;
  country: string;
  min_monthly_salary: { amount: number; currency: string } | null;
  require_disclosed_salary: boolean;
  posted_within_days: number;
  count: number;
  min_score: number;
  use_ai: boolean;
};

export type TaskSpec = { type: string; title: string; agent: string; tool: string; depends_on: string[]; run_always: boolean; approval: boolean };
export type Template = { name: TemplateName; title: string; description: string; tasks: TaskSpec[] };

export type WorkflowStatus =
  | "PENDING"
  | "RUNNING"
  | "NEEDS_INPUT"
  | "AWAITING_APPROVAL"
  | "PAUSED"
  | "SUCCEEDED"
  | "PARTIALLY_SUCCEEDED"
  | "FAILED"
  | "CANCELLED";

export type Report = { headline: string; lines: string[]; next_steps: string[]; items: Item[] };

export type Workflow = {
  id: string;
  template: TemplateName;
  objective: string;
  interpretation: "STRUCTURED" | "AI";
  params: Params;
  plan_version: string;
  status: WorkflowStatus;
  status_reason: string;
  questions: string[];
  report: Report | null;
  created_at: string;
  started_at: string | null;
  completed_at: string | null;
  updated_at: string;
};

export type WorkflowSummary = Workflow & { tasks_total: number; tasks_done: number; pending_approvals: number };

export type Task = {
  id: string;
  seq: number;
  type: string;
  agent: string;
  tool: string;
  expected_output: string;
  required_permissions: string[];
  status: "PENDING" | "READY" | "RUNNING" | "WAITING_APPROVAL" | "NEEDS_INPUT" | "SUCCEEDED" | "FAILED" | "SKIPPED" | "CANCELLED";
  run_always: boolean;
  attempts: number;
  max_attempts: number;
  timeout_seconds: number;
  error_code: string | null;
  error_message: string | null;
  started_at: string | null;
  completed_at: string | null;
};

export type Item = {
  job_id: string;
  title: string;
  company: string;
  score: number;
  application_id?: string;
  document_id?: string;
  validation?: string;
  ok: boolean;
  flags?: string[];
  error?: string;
};

export type Approval = {
  id: string;
  workflow_id: string;
  task_id: string;
  action: string;
  summary: string;
  payload: { items: Item[]; channel: string; note: string };
  payload_hash: string;
  status: "PENDING" | "APPROVED" | "REJECTED" | "EXPIRED" | "CANCELLED";
  decision: { decision: string; approved_job_ids: string[] } | null;
  expires_at: string;
  decided_at: string | null;
  created_at: string;
};

export type ToolCall = {
  id: string;
  task_id: string;
  agent: string;
  tool: string;
  attempt: number;
  outcome: "SUCCEEDED" | "FAILED" | "DENIED" | "NEEDS_INPUT" | "WAITING_APPROVAL";
  error_code: string | null;
  error_message: string | null;
  summary: Record<string, number | string>;
  duration_ms: number;
  created_at: string;
};

export type WorkflowDetail = Workflow & { tasks: Task[]; approvals: Approval[]; tool_calls: ToolCall[] };

export type Interpretation = { proposal: { template: Template; params: Params }; questions: string[]; summary: string; prompt_version: string };

export const agentTitle: Record<string, string> = {
  PROFILE_INTELLIGENCE: "Profile Intelligence",
  DISCOVERY: "Discovery",
  MATCHING: "Matching",
  APPLICATION_PREPARATION: "Application Preparation",
  COMPLIANCE: "Compliance & Safety",
  EXECUTION: "Execution",
  PERFORMANCE: "Performance",
};

export const statusLabel: Record<WorkflowStatus, string> = {
  PENDING: "Pending",
  RUNNING: "Running",
  NEEDS_INPUT: "Needs your input",
  AWAITING_APPROVAL: "Awaiting your approval",
  PAUSED: "Paused",
  SUCCEEDED: "Completed",
  PARTIALLY_SUCCEEDED: "Partly completed",
  FAILED: "Failed",
  CANCELLED: "Cancelled",
};

export const statusTone: Record<WorkflowStatus, TagTone> = {
  PENDING: "outline",
  RUNNING: "stone",
  NEEDS_INPUT: "terracotta",
  AWAITING_APPROVAL: "citron",
  PAUSED: "terracotta",
  SUCCEEDED: "olive",
  PARTIALLY_SUCCEEDED: "stone",
  FAILED: "terracotta",
  CANCELLED: "outline",
};

export const activeStatuses = new Set<WorkflowStatus>(["PENDING", "RUNNING", "NEEDS_INPUT", "AWAITING_APPROVAL", "PAUSED"]);

export const defaultParams: Params = {
  query: "",
  remote_only: false,
  country: "",
  min_monthly_salary: null,
  require_disclosed_salary: false,
  posted_within_days: 30,
  count: 5,
  min_score: 50,
  use_ai: false,
};

export function useTemplates() {
  return useQuery({ queryKey: ["workflow-templates"], queryFn: () => api.get<{ data: Template[]; plan_version: string }>("/api/v1/workflows/templates") });
}

export function useRegistry() {
  return useQuery({
    queryKey: ["agents"],
    queryFn: () =>
      api.get<{
        agents: { name: string; title: string; description: string; tools: string[] }[];
        tools: { name: string; description: string; permission: string; effect: string; timeout_seconds: number; max_attempts: number; idempotency: string; output: string }[];
      }>("/api/v1/agents"),
  });
}

export function useWorkflows() {
  return useQuery({
    queryKey: ["workflows"],
    queryFn: () => api.get<{ data: WorkflowSummary[] }>("/api/v1/workflows"),
    refetchInterval: (q) => (q.state.data?.data.some((w) => w.status === "RUNNING" || w.status === "PENDING") ? 3000 : false),
  });
}

export function useWorkflow(id: string) {
  return useQuery({
    queryKey: ["workflow", id],
    queryFn: () => api.get<WorkflowDetail>(`/api/v1/workflows/${id}`),
    refetchInterval: (q) => (q.state.data && (q.state.data.status === "RUNNING" || q.state.data.status === "PENDING") ? 2000 : false),
  });
}

export function usePendingApprovals() {
  return useQuery({ queryKey: ["approvals", "PENDING"], queryFn: () => api.get<{ data: Approval[] }>("/api/v1/approvals?status=PENDING") });
}

export function useInterpret() {
  return useMutation({ mutationFn: (objective: string) => api.post<Interpretation>("/api/v1/workflows/interpret", { objective }) });
}

export function useStartWorkflow() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: { objective: string; template: TemplateName; params: Params; interpretation: "STRUCTURED" | "AI" }) =>
      api.post<WorkflowDetail>("/api/v1/workflows", v),
    onSuccess: (w) => {
      qc.setQueryData(["workflow", w.id], w);
      void qc.invalidateQueries({ queryKey: ["workflows"] });
    },
  });
}

export function useWorkflowControl(id: string) {
  const qc = useQueryClient();
  const done = (w: WorkflowDetail) => {
    qc.setQueryData(["workflow", id], w);
    void qc.invalidateQueries({ queryKey: ["workflows"] });
    void qc.invalidateQueries({ queryKey: ["approvals"] });
  };
  const call = (action: string) => () => api.post<WorkflowDetail>(`/api/v1/workflows/${id}/${action}`);
  const cancel = useMutation({ mutationFn: call("cancel"), onSuccess: done });
  const pause = useMutation({ mutationFn: call("pause"), onSuccess: done });
  const resume = useMutation({ mutationFn: call("resume"), onSuccess: done });
  const retry = useMutation({ mutationFn: call("retry"), onSuccess: done });
  return { cancel, pause, resume, retry };
}

export function useDecide(workflowId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: { id: string; decision: "APPROVE" | "REJECT"; approved_job_ids: string[]; payload_hash: string }) =>
      api.post(`/api/v1/approvals/${v.id}/decision`, { decision: v.decision, approved_job_ids: v.approved_job_ids, payload_hash: v.payload_hash }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["workflow", workflowId] });
      void qc.invalidateQueries({ queryKey: ["workflows"] });
      void qc.invalidateQueries({ queryKey: ["approvals"] });
      void qc.invalidateQueries({ queryKey: ["applications"] });
    },
  });
}

export type Schedule = {
  id: string;
  name: string;
  objective: string;
  template: TemplateName;
  params: Params;
  cadence: "DAILY" | "WEEKLY";
  weekday: number | null;
  hour: number;
  timezone: string;
  enabled: boolean;
  next_run_at: string;
  last_run_at: string | null;
  last_outcome: "STARTED" | "SKIPPED_PAUSED" | "SKIPPED_OVERLAP" | "SKIPPED_LIMIT" | "FAILED" | null;
  last_message: string | null;
  created_at: string;
};

export type ScheduleBody = Omit<Schedule, "id" | "next_run_at" | "last_run_at" | "last_outcome" | "last_message" | "created_at">;

export function useSchedules() {
  return useQuery({ queryKey: ["schedules"], queryFn: () => api.get<{ data: Schedule[] }>("/api/v1/workflow-schedules") });
}

export function useScheduleActions() {
  const qc = useQueryClient();
  const done = () => void qc.invalidateQueries({ queryKey: ["schedules"] });
  return {
    create: useMutation({ mutationFn: (v: ScheduleBody) => api.post<Schedule>("/api/v1/workflow-schedules", v), onSuccess: done }),
    update: useMutation({ mutationFn: (v: { id: string; body: ScheduleBody }) => api.put<Schedule>(`/api/v1/workflow-schedules/${v.id}`, v.body), onSuccess: done }),
    remove: useMutation({ mutationFn: (id: string) => api.del(`/api/v1/workflow-schedules/${id}`), onSuccess: done }),
  };
}
