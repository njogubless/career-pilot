import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";
import type { MatchBrief } from "./matching";

export type RemoteStatus = "REMOTE" | "HYBRID" | "ONSITE" | "UNKNOWN";
export type RemoteScope = "WORLDWIDE" | "COUNTRIES" | "REGION" | "UNKNOWN" | "NOT_REMOTE";
export type SourceKind = "GREENHOUSE" | "LEVER" | "ASHBY" | "REMOTEOK";

export type Salary = { min: number | null; max: number | null; currency: string; period: "YEAR" | "MONTH" | "HOUR" };

export type JobSummary = {
  id: string;
  title: string;
  company: string;
  department: string;
  location: string;
  remote_status: RemoteStatus;
  remote_scope: RemoteScope;
  remote_countries: string[];
  remote_region: string;
  location_countries: string[];
  employment_type: string;
  salary: Salary | null;
  salary_text: string;
  posted_at: string | null;
  first_seen_at: string;
  source: { kind: SourceKind; name: string; attribution_required: boolean };
  expired_at: string | null;
  /** The viewer's stored match result, when one exists. */
  match: MatchBrief | null;
};

export type JobDetail = JobSummary & {
  description_html: string;
  canonical_url: string;
  apply_url: string;
  tags: string[];
  duplicate_of: string | null;
  repost_of: string | null;
  last_seen_at: string;
};

export type JobPage = { data: JobSummary[]; next_cursor: string | null; total: number };

export type SourceHealth = "HEALTHY" | "DEGRADED" | "PENDING" | "DISABLED";
export type JobSource = {
  id: string;
  kind: SourceKind;
  identifier: string;
  display_name: string;
  enabled: boolean;
  last_success_at: string | null;
  last_run_at: string | null;
  last_error: string | null;
  consecutive_failures: number;
  active_jobs: number;
  health: SourceHealth;
  attribution_required: boolean;
};

export type DiscoveryRun = {
  id: string;
  source_name: string;
  trigger: "SCHEDULED" | "MANUAL";
  status: "RUNNING" | "SUCCEEDED" | "FAILED" | "SKIPPED";
  started_at: string;
  created: number;
  updated: number;
  expired: number;
  error: string | null;
};

export type Overview = {
  jobs_active: number;
  jobs_new_24h: number;
  sources_total: number;
  sources_healthy: number;
  last_discovery_at: string | null;
};

export type SavedSearch = { id: string; name: string; filters: Record<string, unknown>; created_at: string };

/** Search params that are forwarded to GET /jobs (the URL is the source of truth). */
export const FILTER_KEYS = ["q", "remote", "country", "source", "employment", "posted_within", "salary_disclosed", "min_salary", "currency", "sort"] as const;

export function filterQuery(params: URLSearchParams): string {
  const out = new URLSearchParams();
  for (const k of FILTER_KEYS) {
    const v = params.get(k);
    if (v) out.set(k, v);
  }
  return out.toString();
}

export function useJobs(query: string, cursor: string | null) {
  const qs = new URLSearchParams(query);
  if (cursor) qs.set("cursor", cursor);
  qs.set("limit", "20");
  return useQuery({
    queryKey: ["jobs", query, cursor],
    queryFn: () => api.get<JobPage>(`/api/v1/jobs?${qs.toString()}`),
    placeholderData: keepPreviousData,
  });
}

export function useJob(id: string) {
  return useQuery({ queryKey: ["job", id], queryFn: () => api.get<JobDetail>(`/api/v1/jobs/${id}`) });
}

export function useSources() {
  return useQuery({
    queryKey: ["job-sources"],
    queryFn: async () => (await api.get<{ data: JobSource[] }>("/api/v1/job-sources")).data,
    refetchInterval: 60_000,
  });
}

export function useRuns() {
  return useQuery({
    queryKey: ["discovery-runs"],
    queryFn: async () => (await api.get<{ data: DiscoveryRun[] }>("/api/v1/discovery/runs?limit=8")).data,
    refetchInterval: 30_000,
  });
}

export function useOverview() {
  return useQuery({ queryKey: ["dashboard-overview"], queryFn: () => api.get<Overview>("/api/v1/dashboard/overview"), refetchInterval: 60_000 });
}

export function useSavedSearches() {
  return useQuery({
    queryKey: ["saved-searches"],
    queryFn: async () => (await api.get<{ data: SavedSearch[] }>("/api/v1/saved-searches")).data,
  });
}

export function useTriggerDiscovery() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.post<{ queued: boolean; message: string }>("/api/v1/discovery/runs"),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["discovery-runs"] });
      void qc.invalidateQueries({ queryKey: ["job-sources"] });
    },
  });
}

/** Rebuild a URL query string from stored saved-search filters. */
export function savedSearchQuery(f: Record<string, unknown>): string {
  const out = new URLSearchParams();
  const map: Record<string, string> = { q: "q", remote: "remote", country: "country", source: "source", employment: "employment", posted_within: "posted_within", salary_disclosed: "salary_disclosed", min_salary: "min_salary", currency: "currency", sort: "sort" };
  for (const [k, key] of Object.entries(map)) {
    const v = f[k];
    if (v === undefined || v === null || v === "" || v === false || v === 0) continue;
    out.set(key, Array.isArray(v) ? v.join(",") : String(v));
  }
  return out.toString();
}
