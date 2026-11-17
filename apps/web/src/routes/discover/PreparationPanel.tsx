import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { Button } from "../../components/ui/Button";
import { TextArea } from "../../components/ui/Form";
import { Modal } from "../../components/ui/Modal";
import { Panel } from "../../components/ui/Panel";
import { Tag } from "../../components/ui/Tag";
import { apiFieldError } from "../../lib/api";
import { relativeTime } from "../../lib/format";
import { useMeta } from "../../lib/meta";
import { kindLabel, useApplicationDocuments, useGenerateDocument, type AppDocument, type DocKind } from "../../lib/preparation";

const kinds: { kind: DocKind; blurb: string }[] = [
  { kind: "CV", blurb: "Your verified experience, reordered and rephrased for this role." },
  { kind: "COVER_LETTER", blurb: "A short letter citing your evidence. Motivation comes from your notes, never invented." },
  { kind: "APPLICATION_ANSWERS", blurb: "Drafts for the form's questions. Personal or legal questions are left for you." },
];

function approvalTag(d: AppDocument) {
  if (d.approval_state === "CURRENT") return <Tag tone="olive">Approved</Tag>;
  if (d.approval_state === "OUTDATED") return <Tag tone="terracotta">Edited after approval</Tag>;
  if (!d.current_version_id) return <Tag tone="outline">No draft yet</Tag>;
  return <Tag tone="stone">Draft</Tag>;
}

function GenerateModal({ jobId, kind, existing, onClose }: { jobId: string; kind: DocKind; existing?: AppDocument; onClose: () => void }) {
  const navigate = useNavigate();
  const generate = useGenerateDocument(jobId);
  const meta = useMeta();
  const llmReady = meta.data?.integrations.llm?.status === "available";
  const [notes, setNotes] = useState(existing?.notes ?? "");
  const [questions, setQuestions] = useState((existing?.questions ?? []).join("\n"));

  const submit = (source: "AI" | "PROFILE") =>
    generate.mutate(
      {
        kind,
        source,
        notes,
        questions: kind === "APPLICATION_ANSWERS" ? questions.split("\n").map((q) => q.trim()).filter(Boolean) : undefined,
      },
      { onSuccess: (res) => navigate(`/prepare/${res.document.id}`) },
    );

  return (
    <Modal
      title={`${existing?.current_version_id ? "Regenerate" : "Generate"} ${kindLabel[kind].toLowerCase()}`}
      onClose={onClose}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="secondary" icon="person" disabled={generate.isPending} onClick={() => submit("PROFILE")}>
            Start from profile
          </Button>
          <Button icon="auto_awesome" iconClassName="text-secondary-fixed" disabled={generate.isPending || !llmReady} onClick={() => submit("AI")}>
            {generate.isPending ? "Working…" : "Generate with AI"}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-space-md">
        <p className="text-body-md text-on-surface-variant">
          Both options use only <strong>verified</strong> facts from your profile; unverified imports are left out. <em>Start from profile</em> copies
          your facts as they are for you to tailor. <em>Generate with AI</em> tailors them to this job, and any statement your profile cannot support
          is removed before you see it. Nothing is sent to the employer.
        </p>
        {meta.data && !llmReady && (
          <p className="border-l-2 border-terracotta pl-space-sm text-body-sm text-on-surface">
            No AI provider is configured on this server, so AI drafts are unavailable. You can still start from your profile.
          </p>
        )}
        {existing?.current_version_id && (
          <p className="text-body-sm text-muted">Your current version is kept in the history. The new draft becomes a new version.</p>
        )}
        {kind === "APPLICATION_ANSWERS" && (
          <TextArea
            label="Application questions (one per line)"
            rows={5}
            value={questions}
            onChange={(e) => setQuestions(e.target.value)}
            placeholder={"Describe a system you designed and its impact.\nWhy do you want to work here?"}
            error={apiFieldError(generate.error, "questions")}
          />
        )}
        <TextArea
          label="Notes for this application (optional)"
          hint="Facts that aren't in your profile, such as why you want this role. They are treated as your own statements and cited like other facts."
          rows={4}
          maxLength={4000}
          value={notes}
          onChange={(e) => setNotes(e.target.value)}
          error={apiFieldError(generate.error, "notes")}
        />
        {generate.isError && !apiFieldError(generate.error, "questions") && !apiFieldError(generate.error, "notes") && (
          <p role="alert" className="text-body-sm text-terracotta">
            {generate.error.message}
          </p>
        )}
      </div>
    </Modal>
  );
}

/** Job page entry point for tailored documents. */
export function PreparationPanel({ jobId }: { jobId: string }) {
  const docs = useApplicationDocuments(jobId);
  const [open, setOpen] = useState<DocKind | null>(null);
  const byKind = new Map((docs.data?.data ?? []).map((d) => [d.kind, d]));

  return (
    <Panel>
      <div className="border-b border-stone p-space-md">
        <p className="label-caps text-outline">Application preparation</p>
        <h2 className="font-serif text-headline-sm text-on-surface">Tailored documents</h2>
      </div>
      {docs.isError && (
        <p role="alert" className="p-space-md text-body-sm text-terracotta">
          {docs.error.message}
        </p>
      )}
      <ul className="divide-y divide-stone">
        {kinds.map(({ kind, blurb }) => {
          const d = byKind.get(kind);
          return (
            <li key={kind} className="flex flex-col gap-space-xs p-space-md">
              <div className="flex items-center justify-between gap-space-sm">
                <span className="font-semibold text-on-surface">{kindLabel[kind]}</span>
                {d ? approvalTag(d) : <Tag tone="outline">Not started</Tag>}
              </div>
              <p className="text-body-sm text-on-surface-variant">{blurb}</p>
              <div className="flex flex-wrap items-center gap-space-sm">
                {d && (
                  <Link
                    to={`/prepare/${d.id}`}
                    className="inline-flex items-center gap-1 rounded-sm border border-stone px-space-sm py-1 text-[13px] font-semibold text-on-surface hover:border-graphite"
                  >
                    Open editor
                  </Link>
                )}
                <Button variant={d ? "ghost" : "secondary"} icon="auto_awesome" className="py-1 text-[13px]" onClick={() => setOpen(kind)}>
                  {d?.current_version_id ? "Regenerate" : "Generate"}
                </Button>
                {d && <span className="font-mono text-label-sm text-muted">updated {relativeTime(d.updated_at)}</span>}
              </div>
            </li>
          );
        })}
      </ul>
      {open && <GenerateModal jobId={jobId} kind={open} existing={byKind.get(open)} onClose={() => setOpen(null)} />}
    </Panel>
  );
}
