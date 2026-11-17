import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";
import { Button } from "../../components/ui/Button";
import { FormError } from "../../components/ui/Form";
import { Icon } from "../../components/ui/Icon";
import { Loading } from "../../components/ui/Loading";
import { EmptyState, Panel } from "../../components/ui/Panel";
import { Tag } from "../../components/ui/Tag";
import { api } from "../../lib/api";
import { documentsKey, formatBytes, useDocuments, useExtraction, type Doc, type Extraction } from "../../lib/documents";
import { useMeta } from "../../lib/meta";
import { useProfile } from "../../lib/profile";
import { ExtractionReview } from "./ExtractionReview";
import { TailoredDocuments } from "./TailoredDocuments";

const MAX_BYTES = 10 * 1024 * 1024;

function UploadPanel({ disabled }: { disabled: boolean }) {
  const qc = useQueryClient();
  const input = useRef<HTMLInputElement>(null);
  const [drag, setDrag] = useState(false);
  const [clientError, setClientError] = useState<string | null>(null);
  const upload = useMutation({
    mutationFn: (file: File) => {
      const form = new FormData();
      form.append("kind", "CV");
      form.append("file", file);
      return api.upload<Doc>("/api/v1/documents", form);
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: documentsKey }),
  });

  const pick = (file: File | undefined) => {
    setClientError(null);
    if (!file) return;
    if (!/\.(pdf|docx)$/i.test(file.name)) return setClientError("Only PDF and DOCX files are supported.");
    if (file.size > MAX_BYTES) return setClientError("Files must be at most 10 MB.");
    upload.mutate(file);
  };

  return (
    <Panel>
      <div
        onDragOver={(e) => {
          e.preventDefault();
          if (!disabled) setDrag(true);
        }}
        onDragLeave={() => setDrag(false)}
        onDrop={(e) => {
          e.preventDefault();
          setDrag(false);
          if (!disabled) pick(e.dataTransfer.files[0]);
        }}
        className={"m-space-md flex flex-col items-start gap-space-sm rounded-sm border border-dashed p-space-lg " + (drag ? "border-graphite bg-surface-container-low" : "border-stone")}
      >
        <Icon name="upload_file" size={24} className="text-olive" />
        <h2 className="font-serif text-headline-sm text-on-surface">Upload your CV</h2>
        <p className="text-body-sm text-on-surface-variant">PDF or Word (.docx), up to 10 MB. Stored privately; only you can download it.</p>
        <input ref={input} type="file" accept=".pdf,.docx" className="sr-only" onChange={(e) => pick(e.target.files?.[0])} aria-label="Choose a CV file" />
        <Button icon="attach_file" disabled={disabled || upload.isPending} onClick={() => input.current?.click()}>
          {upload.isPending ? "Uploading…" : "Choose file"}
        </Button>
        {disabled && <p className="text-body-sm text-terracotta">Document storage is not configured on this server, so uploads are disabled.</p>}
        {clientError && (
          <p role="alert" className="text-body-sm text-terracotta">
            {clientError}
          </p>
        )}
        <FormError error={upload.error} />
      </div>
    </Panel>
  );
}

function ExtractionStatus({ ex }: { ex: Extraction }) {
  switch (ex.status) {
    case "QUEUED":
    case "RUNNING":
      return <Loading label={ex.status === "QUEUED" ? "Queued for analysis" : "Analysing CV"} />;
    case "FAILED":
      return (
        <p role="alert" className="text-body-sm text-terracotta">
          Analysis failed: {ex.error_message ?? "unknown error"}
        </p>
      );
    case "APPLIED":
      return <Tag tone="olive">Applied to profile</Tag>;
    case "DISCARDED":
      return <Tag tone="outline">Discarded</Tag>;
    default:
      return null;
  }
}

function DocumentRow({ doc, aiReady, onReview }: { doc: Doc; aiReady: boolean; onReview: (id: string) => void }) {
  const qc = useQueryClient();
  const [extractionId, setExtractionId] = useState<string | null>(null);
  const ex = useExtraction(extractionId);
  const start = useMutation({
    mutationFn: () => api.post<Extraction>(`/api/v1/documents/${doc.id}/extractions`),
    onSuccess: (e) => setExtractionId(e.id),
  });
  const del = useMutation({ mutationFn: () => api.del(`/api/v1/documents/${doc.id}`), onSuccess: () => qc.invalidateQueries({ queryKey: documentsKey }) });
  const status = ex.data;

  return (
    <li className="flex flex-col gap-space-sm p-space-md">
      <div className="flex flex-col gap-space-sm sm:flex-row sm:items-center sm:justify-between">
        <div className="flex min-w-0 items-center gap-space-sm">
          <Icon name="description" className="text-outline" />
          <div className="min-w-0">
            <p className="truncate text-title-md text-on-surface">{doc.original_filename}</p>
            <p className="font-mono text-label-sm text-muted">
              {doc.kind} · {formatBytes(doc.size_bytes)} · {new Date(doc.created_at).toLocaleDateString()}
            </p>
          </div>
        </div>
        <div className="flex flex-wrap gap-1">
          {doc.kind === "CV" && (
            <Button
              variant="primary"
              icon="auto_fix_high"
              iconClassName="text-secondary-fixed"
              disabled={!aiReady || start.isPending || status?.status === "QUEUED" || status?.status === "RUNNING"}
              title={aiReady ? undefined : "No AI provider is configured on this server"}
              onClick={() => start.mutate()}
            >
              Analyse CV
            </Button>
          )}
          <a
            href={`/api/v1/documents/${doc.id}/download`}
            className="inline-flex items-center gap-1 rounded-sm border border-stone px-space-md py-2 text-[14px] font-semibold text-graphite hover:border-graphite"
          >
            <Icon name="download" size={16} /> Download
          </a>
          <Button
            variant="ghost"
            className="text-terracotta"
            disabled={del.isPending}
            onClick={() => {
              if (window.confirm(`Delete ${doc.original_filename}? This also removes any pending extraction.`)) del.mutate();
            }}
          >
            Delete
          </Button>
        </div>
      </div>
      <FormError error={start.error ?? del.error} />
      {status && <ExtractionStatus ex={status} />}
      {status?.status === "PENDING_REVIEW" && (
        <div>
          <Button variant="accent" icon="fact_check" onClick={() => onReview(status.id)}>
            Review extracted profile
          </Button>
        </div>
      )}
    </li>
  );
}

export function DocumentsPage() {
  const docs = useDocuments();
  const meta = useMeta();
  const profile = useProfile();
  const [reviewId, setReviewId] = useState<string | null>(null);
  const review = useExtraction(reviewId);
  const storageReady = meta.data?.integrations.document_storage?.status === "available";
  const aiReady = meta.data?.integrations.llm?.status === "available";

  return (
    <div className="flex flex-col gap-space-lg">
      <header className="flex flex-col gap-space-xs">
        <Tag tone="graphite" className="self-start">
          Dossier & systems
        </Tag>
        <h1 className="font-serif text-headline-lg-mobile text-on-surface md:text-headline-lg">Documents & CVs</h1>
        <p className="max-w-2xl text-body-lg text-on-surface-variant">
          Upload a CV and CareerPilot proposes profile entries from it. You review every item before anything is saved. Tailored documents for
          specific jobs are listed below.
        </p>
        {meta.data && !aiReady && (
          <p className="max-w-2xl border-l-2 border-terracotta pl-space-sm text-body-sm text-on-surface">
            No AI provider is configured on this server, so CV analysis is unavailable. You can still upload documents and enter your profile by
            hand.
          </p>
        )}
      </header>

      {reviewId && review.data?.status === "PENDING_REVIEW" && profile.data ? (
        <ExtractionReview extraction={review.data} basics={profile.data.basics} onDone={() => setReviewId(null)} />
      ) : (
        <div className="grid grid-cols-1 items-start gap-space-lg xl:grid-cols-12">
          <div className="xl:col-span-5">
            <UploadPanel disabled={meta.isSuccess && !storageReady} />
          </div>
          <Panel className="xl:col-span-7">
            <div className="p-space-md">
              <p className="label-caps text-outline">Private vault</p>
              <h2 className="font-serif text-headline-sm text-on-surface">Your documents</h2>
            </div>
            {docs.isPending && <Loading />}
            {docs.isError && (
              <p role="alert" className="p-space-md text-terracotta">
                {docs.error.message}
              </p>
            )}
            {docs.data?.length === 0 && (
              <EmptyState icon="folder_open" title="No documents yet">
                Upload your CV to get started.
              </EmptyState>
            )}
            {docs.data && docs.data.length > 0 && (
              <ul className="divide-y divide-stone border-t border-stone">
                {docs.data.map((d) => (
                  <DocumentRow key={d.id} doc={d} aiReady={aiReady} onReview={setReviewId} />
                ))}
              </ul>
            )}
          </Panel>
        </div>
      )}
      <TailoredDocuments />
    </div>
  );
}
