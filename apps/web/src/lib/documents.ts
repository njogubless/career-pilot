import { useQuery } from "@tanstack/react-query";
import { api } from "./api";

export type DocumentKind = "CV" | "COVER_LETTER" | "EVIDENCE";
export type Doc = {
  id: string;
  kind: DocumentKind;
  original_filename: string;
  mime_type: string;
  size_bytes: number;
  created_at: string;
};

export type ExtractionStatus = "QUEUED" | "RUNNING" | "PENDING_REVIEW" | "APPLIED" | "DISCARDED" | "FAILED";

// Proposal shape produced by the AI service (services/ai/app/extraction/schemas.py).
export type ProposalExperience = {
  employer: string;
  title: string;
  location: string | null;
  start_date: string | null;
  end_date: string | null;
  is_current: boolean;
  description: string | null;
  achievements: string[];
  evidence: string;
  evidence_found: boolean;
};
export type ProposalEducation = {
  institution: string;
  degree: string | null;
  field: string | null;
  start_date: string | null;
  end_date: string | null;
  evidence: string;
  evidence_found: boolean;
};
export type ProposalSkill = { name: string; category: string | null; evidence: string; evidence_found: boolean };
export type ProposalCertification = {
  name: string;
  issuer: string | null;
  issued_date: string | null;
  evidence: string;
  evidence_found: boolean;
};
export type ProposalProject = {
  name: string;
  description: string | null;
  url: string | null;
  evidence: string;
  evidence_found: boolean;
};
export type Proposal = {
  personal: {
    full_name: string | null;
    headline: string | null;
    summary: string | null;
    location_city: string | null;
    location_country: string | null;
    links: { label: string; url: string }[];
    found: Record<string, boolean>;
  };
  experiences: ProposalExperience[];
  education: ProposalEducation[];
  skills: ProposalSkill[];
  certifications: ProposalCertification[];
  projects: ProposalProject[];
  warnings: string[];
};

export type Extraction = {
  id: string;
  document_id: string;
  status: ExtractionStatus;
  proposal?: Proposal;
  error_code?: string;
  error_message?: string;
  provider?: string;
  model?: string;
  prompt_version?: string;
  created_at: string;
  updated_at: string;
};

export const documentsKey = ["documents"] as const;

export function useDocuments() {
  return useQuery({
    queryKey: documentsKey,
    queryFn: async () => (await api.get<{ data: Doc[] }>("/api/v1/documents")).data,
  });
}

const ACTIVE: ExtractionStatus[] = ["QUEUED", "RUNNING"];

/** Polls an extraction while it is queued or running. */
export function useExtraction(id: string | null) {
  return useQuery({
    queryKey: ["extraction", id],
    queryFn: () => api.get<Extraction>(`/api/v1/profile-extractions/${id}`),
    enabled: id !== null,
    refetchInterval: (q) => (q.state.data && ACTIVE.includes(q.state.data.status) ? 2000 : false),
  });
}

/** "YYYY" or "YYYY-MM" from a CV → first day of that period, as the API expects. */
export function toApiDate(v: string | null): string | null {
  if (!v) return null;
  if (/^\d{4}-\d{2}$/.test(v)) return `${v}-01`;
  if (/^\d{4}$/.test(v)) return `${v}-01-01`;
  if (/^\d{4}-\d{2}-\d{2}$/.test(v)) return v;
  return null;
}

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}
