import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";

/** Career analytics (Phase 9): every number is derived from stored records. */

export type Rate = { numerator: number; denominator: number; value: number | null; low_sample: boolean };
export type Stage = { key: string; label: string; count: number; from_previous: Rate | null };

export type Overview = {
  period_days: number;
  since: string | null;
  funnel: { jobs_discovered: number; jobs_matched: number; stages: Stage[]; rejected: number; withdrawn: number };
  durations: { key: string; label: string; median_days: number | null; samples: number }[];
  outreach: { introductions_sent: number; follow_ups_sent: number; introductions_replied: number; bounced: number; reply_rate: Rate };
  sources: { source: string; kind: string; jobs_matched: number; applications: number; submitted: number; responses: number; interviews: number; response_rate: Rate }[];
  skill_weeks: string[];
  skill_trends: { skill: string; total: number; share: number[] }[];
  weekly: { start: string; applications: number; submitted: number; responses: number; outreach_sent: number; strong_matches: number }[];
  computed_at: string;
};

export type Report = {
  id: string;
  period_start: string;
  period_end: string;
  trigger: "SCHEDULED" | "MANUAL";
  created_at: string;
  data: {
    strong_matches: number;
    shortlisted: number;
    submitted: number;
    responses: number;
    interviews: number;
    offers: number;
    rejections: number;
    outreach_sent: number;
    outreach_replies: number;
    top_skill_gaps: { skill: string; total: number }[];
    highlights: string[];
    recommendations: string[];
  };
};

export function useOverview(period: number) {
  return useQuery({ queryKey: ["analytics", period], queryFn: () => api.get<Overview>(`/api/v1/analytics/overview?period=${period}`) });
}

export function useReports() {
  return useQuery({ queryKey: ["reports"], queryFn: () => api.get<{ data: Report[] }>("/api/v1/analytics/reports") });
}

export function useGenerateReport() {
  const qc = useQueryClient();
  return useMutation({ mutationFn: () => api.post<Report>("/api/v1/analytics/reports"), onSuccess: () => void qc.invalidateQueries({ queryKey: ["reports"] }) });
}

export function pct(r: Rate | null | undefined) {
  return r?.value == null ? "—" : `${Math.round(r.value * 100)}%`;
}
