import { useState } from "react";
import { useNavigate } from "react-router";
import { Button } from "../../components/ui/Button";
import { TextArea } from "../../components/ui/Form";
import { Modal } from "../../components/ui/Modal";
import { useMeta } from "../../lib/meta";
import { useCreateDraft, type Contact } from "../../lib/outreach";
import { useApplicationDocuments } from "../../lib/preparation";

/** Start an email to a contact: AI draft (grounded) or a plain template. Nothing is sent. */
export function DraftDialog({ contact, jobId, onClose }: { contact: Contact; jobId?: string | null; onClose: () => void }) {
  const navigate = useNavigate();
  const draft = useCreateDraft();
  const meta = useMeta();
  const docs = useApplicationDocuments();
  const llm = meta.data?.integrations.llm?.status === "available";
  const [notes, setNotes] = useState("");
  const job = jobId ?? contact.job_id;
  const cvs = (docs.data?.data ?? []).filter((d) => d.kind === "CV" && d.approved_version_id && (!job || d.job_id === job));
  const [attach, setAttach] = useState<string>("");
  const start = (mode: "AI" | "TEMPLATE") =>
    draft.mutate(
      { contact_id: contact.id, job_id: job, mode, notes, attachment_version_id: attach || null },
      { onSuccess: (m) => navigate(`/outreach/messages/${m.id}`) },
    );
  return (
    <Modal
      title={`Email ${contact.name || contact.email}`}
      onClose={onClose}
      footer={
        <>
          <Button variant="secondary" disabled={draft.isPending} onClick={() => start("TEMPLATE")}>
            Start from template
          </Button>
          <Button icon="auto_awesome" iconClassName="text-secondary-fixed" disabled={draft.isPending || !llm} onClick={() => start("AI")}>
            Draft with AI
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-space-sm">
        <p className="text-body-md text-on-surface-variant">
          You will review and edit the email before approving it. Nothing is sent until you approve it and press Send.
        </p>
        {meta.data && !llm && <p className="text-body-sm text-muted">No AI provider is configured, so only the template is available.</p>}
        <TextArea
          label="Notes (optional)"
          hint="Anything the email should say that is not in your profile, such as why this company. Cited as your own words."
          rows={3}
          maxLength={2000}
          value={notes}
          onChange={(e) => setNotes(e.target.value)}
        />
        <label className="flex flex-col gap-1 text-[12px] font-medium text-on-surface-variant">
          Attach an approved CV (optional)
          <select value={attach} onChange={(e) => setAttach(e.target.value)} className="rounded-sm border border-stone bg-surface-container-lowest px-2 py-1.5 text-body-md text-on-surface">
            <option value="">No attachment</option>
            {cvs.map((d) => (
              <option key={d.id} value={d.approved_version_id!}>
                Tailored CV, {d.company} · {d.job_title}
              </option>
            ))}
          </select>
        </label>
        {draft.isError && <p role="alert" className="text-body-sm text-terracotta">{draft.error.message}</p>}
      </div>
    </Modal>
  );
}
