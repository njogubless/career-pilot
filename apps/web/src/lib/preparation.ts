import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { TagTone } from "../components/ui/Tag";
import { api } from "./api";

/** Application preparation (Phase 5): tailored documents built from verified facts. */

export type DocKind = "CV" | "COVER_LETTER" | "APPLICATION_ANSWERS";
export type BlockType = "contact" | "heading" | "entry" | "paragraph" | "bullet" | "skills" | "answer";

export type Block = { id: string; type: BlockType; text: string; refs: string[]; meta: Record<string, string> };
export type Section = { key: string; title: string; blocks: Block[] };
export type Content = { schema_version: "doc-v1"; kind: DocKind; sections: Section[] };

export type Issue = { block_id: string; code: string; severity: "ERROR" | "WARNING"; message: string; excerpt: string };
export type Validation = {
  validator_version: string;
  status: "PASSED" | "WARNINGS" | "FAILED";
  issues: Issue[];
  claims_checked: number;
};
export type ValidationStatus = Validation["status"] | "NOT_VALIDATED";

export type VersionMeta = {
  id: string;
  version_no: number;
  origin: "GENERATED" | "TEMPLATE" | "USER_EDIT" | "RESTORED";
  restored_from: string | null;
  validation_status: ValidationStatus;
  prompt_version: string | null;
  model: string | null;
  created_at: string;
};

export type Version = VersionMeta & {
  content: Content;
  validation: Validation | null;
  questions: string[];
  warnings: string[];
  facts_changed: boolean;
};

export type Generation = {
  id: string;
  status: "QUEUED" | "RUNNING" | "SUCCEEDED" | "FAILED";
  version_id: string | null;
  error_code: string | null;
  error_message: string | null;
  created_at: string;
  updated_at: string;
};

export type AppDocument = {
  id: string;
  job_id: string;
  kind: DocKind;
  title: string;
  notes: string;
  questions: string[];
  current_version_id: string | null;
  approved_version_id: string | null;
  approved_at: string | null;
  approval_state: "NONE" | "CURRENT" | "OUTDATED";
  job_title: string;
  company: string;
  created_at: string;
  updated_at: string;
};

export type FactRef = { ref: string; kind: string; text: string };

export type DocumentView = AppDocument & {
  current_version: Version | null;
  latest_generation: Generation | null;
  facts: FactRef[];
};

export const kindLabel: Record<DocKind, string> = {
  CV: "Tailored CV",
  COVER_LETTER: "Cover letter",
  APPLICATION_ANSWERS: "Application answers",
};

export const originLabel: Record<VersionMeta["origin"], string> = {
  GENERATED: "AI draft",
  TEMPLATE: "From profile",
  USER_EDIT: "Your edit",
  RESTORED: "Restored",
};

export const statusTone: Record<ValidationStatus, TagTone> = {
  PASSED: "olive",
  WARNINGS: "stone",
  FAILED: "terracotta",
  NOT_VALIDATED: "outline",
};
export const statusLabel: Record<ValidationStatus, string> = {
  PASSED: "Fact check passed",
  WARNINGS: "Passed with warnings",
  FAILED: "Unsupported statements",
  NOT_VALIDATED: "Not fact-checked",
};

const inFlight = (g: Generation | null | undefined) => g?.status === "QUEUED" || g?.status === "RUNNING";

const base = "/api/v1/application-documents";

export function useApplicationDocuments(jobId?: string) {
  return useQuery({
    queryKey: ["app-docs", jobId ?? "all"],
    queryFn: () => api.get<{ data: AppDocument[] }>(jobId ? `${base}?job_id=${jobId}` : base),
  });
}

export function useApplicationDocument(id: string) {
  return useQuery({
    queryKey: ["app-doc", id],
    queryFn: () => api.get<DocumentView>(`${base}/${id}`),
    refetchInterval: (q) => (inFlight(q.state.data?.latest_generation) ? 3000 : false),
  });
}

export function useVersions(id: string) {
  return useQuery({
    queryKey: ["app-doc-versions", id],
    queryFn: () => api.get<{ data: VersionMeta[] }>(`${base}/${id}/versions`),
  });
}

export function useVersion(id: string, versionId: string | null) {
  return useQuery({
    queryKey: ["app-doc-version", id, versionId],
    queryFn: () => api.get<Version>(`${base}/${id}/versions/${versionId}`),
    enabled: !!versionId,
  });
}

function useInvalidate(id?: string) {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: ["app-docs"] });
    if (id) {
      void qc.invalidateQueries({ queryKey: ["app-doc", id] });
      void qc.invalidateQueries({ queryKey: ["app-doc-versions", id] });
    }
  };
}

export type GenerateBody = { kind: DocKind; source: "AI" | "PROFILE"; notes?: string; questions?: string[] };

export function useGenerateDocument(jobId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: GenerateBody) =>
      api.post<{ document: AppDocument; generation: Generation | null }>(`/api/v1/jobs/${jobId}/application-documents`, body),
    onSuccess: (res) => {
      void qc.invalidateQueries({ queryKey: ["app-docs"] });
      void qc.invalidateQueries({ queryKey: ["app-doc", res.document.id] });
    },
  });
}

export function useSaveContent(id: string) {
  const done = useInvalidate(id);
  return useMutation({
    mutationFn: (body: { base_version_id: string; content: Content }) => api.put<Version>(`${base}/${id}/content`, body),
    onSuccess: done,
  });
}

export function useRestoreVersion(id: string) {
  const done = useInvalidate(id);
  return useMutation({
    mutationFn: (versionId: string) => api.post<Version>(`${base}/${id}/versions/${versionId}/restore`),
    onSuccess: done,
  });
}

export function useRecheck(id: string) {
  return useMutation({ mutationFn: () => api.post<Validation>(`${base}/${id}/recheck`) });
}

export function useApprove(id: string) {
  const done = useInvalidate(id);
  return useMutation({
    mutationFn: (body: { version_id: string; acknowledge_issues: boolean }) =>
      api.post<{ document: AppDocument; validation: Validation }>(`${base}/${id}/approval`, body),
    onSuccess: done,
  });
}

export function useRevokeApproval(id: string) {
  const done = useInvalidate(id);
  return useMutation({ mutationFn: () => api.del<AppDocument>(`${base}/${id}/approval`), onSuccess: done });
}

export function useDeleteDocument(id: string) {
  const done = useInvalidate();
  return useMutation({ mutationFn: () => api.del(`${base}/${id}`), onSuccess: done });
}

export function exportUrl(id: string, versionId: string, format: "docx" | "txt" | "md") {
  return `${base}/${id}/versions/${versionId}/export?format=${format}`;
}

/** Errors that must be fixed (cannot be acknowledged) before approval. */
export const blockingCodes = new Set(["NEEDS_INPUT", "EMPTY", "PLACEHOLDER"]);

/** Block types the user writes; the rest are rendered from the profile. */
export const editableTypes = new Set<BlockType>(["paragraph", "bullet", "answer"]);

let seq = 0;
export function newBlockId() {
  seq += 1;
  return `u${Date.now().toString(36)}${seq}`;
}
