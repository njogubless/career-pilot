import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";

export type Eligibility = "ELIGIBLE" | "INELIGIBLE" | "UNKNOWN";
export type Confidence = "HIGH" | "MEDIUM" | "LOW";
export type CheckStatus = "PASS" | "FAIL" | "UNKNOWN";

export type MatchBrief = { score: number; eligibility: Eligibility; confidence: Confidence };

export type Component = { key: string; label: string; score: number | null; detail: string };
export type Dimension = { key: string; label: string; weight: number; score: number | null; summary: string; components: Component[] };
export type Check = { key: string; label: string; status: CheckStatus; critical: boolean; detail: string; evidence?: string };
export type SkillMatch = {
  name: string;
  slug?: string;
  importance: "REQUIRED" | "PREFERRED" | "MENTIONED";
  weight: number;
  status: "MATCHED" | "PARTIAL" | "MISSING" | "UNKNOWN";
  credit: number;
  job_evidence: string;
  profile_source?: "SKILL_VERIFIED" | "SKILL_UNVERIFIED" | "EXPERIENCE_VERIFIED" | "EXPERIENCE_UNVERIFIED";
  profile_evidence?: string;
};
export type Signal = { kind: "POSITIVE" | "NEGATIVE"; text: string };

export type MatchResult = {
  algorithm_version: string;
  score: number;
  eligibility: Eligibility;
  confidence: Confidence;
  known_weight: number;
  analysis_method: "DETERMINISTIC" | "LLM";
  analyzer_version: string;
  dimensions: Dimension[];
  checks: Check[];
  skills: SkillMatch[];
  missing_skills: string[];
  missing_preferred: string[];
  blockers: string[];
  signals: Signal[];
  unknowns: string[];
  explanation: string[];
};

export type AnalysisInfo = {
  method: "LLM" | "DETERMINISTIC" | "NONE";
  analyzer_version: string;
  analyzed_at: string | null;
  model: string | null;
  deep_status: "NONE" | "QUEUED" | "RUNNING" | "SUCCEEDED" | "FAILED";
  deep_error: string | null;
  embedded: boolean;
};

export type Feedback = {
  rating: "UP" | "DOWN";
  reason: string | null;
  note: string;
  dismissed: boolean;
  updated_at: string;
};

export type JobMatchResponse = {
  status: "READY" | "PROFILE_INCOMPLETE" | "ANALYSIS_PENDING" | "AI_UNAVAILABLE";
  message?: string;
  match: MatchResult | null;
  analysis: AnalysisInfo;
  feedback: Feedback | null;
  deep_analysis_available: boolean;
  computed_at: string | null;
  disclaimer: string;
};

export type MatchState = {
  status: "NOT_STARTED" | "QUEUED" | "RUNNING" | "READY" | "FAILED";
  algorithm_version: string;
  candidates: number;
  scored: number;
  error: string | null;
  started_at: string | null;
  completed_at: string | null;
};

export type SkillGap = { skill: string; jobs: number };

export function useJobMatch(jobId: string) {
  return useQuery({
    queryKey: ["job-match", jobId],
    queryFn: () => api.get<JobMatchResponse>(`/api/v1/jobs/${jobId}/match`),
    // Poll while a deep analysis is in flight.
    refetchInterval: (q) => {
      const s = q.state.data?.analysis.deep_status;
      return s === "QUEUED" || s === "RUNNING" ? 5000 : false;
    },
  });
}

export function useMatchState() {
  return useQuery({
    queryKey: ["match-state"],
    queryFn: () => api.get<MatchState>("/api/v1/matches/status"),
    refetchInterval: (q) => (q.state.data?.status === "QUEUED" || q.state.data?.status === "RUNNING" ? 5000 : 60_000),
  });
}

export function useSkillGaps() {
  return useQuery({
    queryKey: ["skill-gaps"],
    queryFn: () => api.get<{ data: SkillGap[]; matches_considered: number }>("/api/v1/matches/skill-gaps"),
  });
}

export function useRecomputeMatches() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.post<{ queued: boolean; message: string }>("/api/v1/matches/recompute"),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["match-state"] }),
  });
}

export function useRequestDeepAnalysis(jobId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.post<{ queued: boolean; message: string }>(`/api/v1/jobs/${jobId}/analysis`),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["job-match", jobId] }),
  });
}

export function useMatchFeedback(jobId: string) {
  const qc = useQueryClient();
  const done = () => {
    void qc.invalidateQueries({ queryKey: ["job-match", jobId] });
    void qc.invalidateQueries({ queryKey: ["jobs"] });
  };
  const save = useMutation({
    mutationFn: (body: { rating: "UP" | "DOWN"; reason?: string | null; note?: string; dismissed?: boolean }) =>
      api.put<Feedback>(`/api/v1/jobs/${jobId}/match/feedback`, body),
    onSuccess: done,
  });
  const clear = useMutation({ mutationFn: () => api.del(`/api/v1/jobs/${jobId}/match/feedback`), onSuccess: done });
  return { save, clear };
}

export const eligibilityLabel: Record<Eligibility, string> = {
  ELIGIBLE: "Eligible",
  INELIGIBLE: "Not eligible",
  UNKNOWN: "Eligibility unconfirmed",
};
