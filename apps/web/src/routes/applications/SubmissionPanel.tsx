import { useRef, useState } from "react";
import { Button } from "../../components/ui/Button";
import { TextArea, TextInput } from "../../components/ui/Form";
import { Icon } from "../../components/ui/Icon";
import { Panel } from "../../components/ui/Panel";
import { api, apiFieldError } from "../../lib/api";
import { newIdempotencyKey, useApplicationActions, useStartSubmission, type AppDetail } from "../../lib/applications";
import { exportUrl, kindLabel } from "../../lib/preparation";

const failureReasons: { value: string; label: string }[] = [
  { value: "VERIFICATION_REQUIRED", label: "The site asked for a CAPTCHA, login or other verification I could not complete" },
  { value: "SITE_ERROR", label: "The employer's site returned an error" },
  { value: "POSTING_CLOSED", label: "The posting is closed" },
  { value: "INELIGIBLE", label: "The site's screening said I'm not eligible" },
  { value: "OTHER", label: "Something else" },
];

/** The approved package, as exports of the pinned versions. */
function PackageDownloads({ app }: { app: AppDetail }) {
  const pins = [app.pinned.cv_version_id, app.pinned.cover_letter_version_id, app.pinned.answers_version_id];
  const docs = app.package.documents.filter((d) => d.approved_version_id && pins.includes(d.approved_version_id));
  return (
    <ul className="flex flex-col gap-1">
      {docs.map((d) => (
        <li key={d.document_id} className="flex flex-wrap items-center gap-space-sm text-body-md">
          <span className="text-on-surface">
            {kindLabel[d.kind]} v{d.approved_version_no}
          </span>
          {(["docx", "txt"] as const).map((f) => (
            <a key={f} href={exportUrl(d.document_id, d.approved_version_id!, f)} download className="font-mono text-label-md underline">
              {f.toUpperCase()}
            </a>
          ))}
        </li>
      ))}
    </ul>
  );
}

function ConfirmForm({ app, attemptId }: { app: AppDetail; attemptId: string }) {
  const { confirm } = useApplicationActions(app.id);
  const [attest, setAttest] = useState(false);
  const [reference, setReference] = useState("");
  const [note, setNote] = useState("");
  const [uploadError, setUploadError] = useState<string | null>(null);
  const [uploading, setUploading] = useState(false);
  const file = useRef<HTMLInputElement>(null);

  const submit = async () => {
    setUploadError(null);
    let document_id: string | undefined;
    const f = file.current?.files?.[0];
    if (f) {
      setUploading(true);
      try {
        const form = new FormData();
        form.append("kind", "EVIDENCE");
        form.append("file", f);
        document_id = (await api.upload<{ id: string }>("/api/v1/documents", form)).id;
      } catch (e) {
        setUploadError(e instanceof Error ? e.message : "Upload failed.");
        setUploading(false);
        return;
      }
      setUploading(false);
    }
    confirm.mutate({ attemptId, body: { attest, reference, note, document_id } });
  };

  return (
    <div className="flex flex-col gap-space-sm">
      <TextInput
        label="Confirmation reference (optional)"
        hint="For example the confirmation number or the text of the confirmation page."
        value={reference}
        maxLength={500}
        onChange={(e) => setReference(e.target.value)}
        error={apiFieldError(confirm.error, "reference")}
      />
      <TextArea label="Note (optional)" rows={2} value={note} maxLength={2000} onChange={(e) => setNote(e.target.value)} />
      <label className="flex flex-col gap-1 text-[12px] font-medium text-on-surface-variant">
        Screenshot or PDF of the confirmation (optional, PDF or DOCX)
        <input ref={file} type="file" accept=".pdf,.docx" className="text-body-sm" />
      </label>
      {uploadError && <p className="text-body-sm text-terracotta">{uploadError}</p>}
      <label className="flex items-start gap-space-xs text-body-md text-on-surface">
        <input type="checkbox" checked={attest} onChange={(e) => setAttest(e.target.checked)} className="mt-1" />
        I submitted this application on the employer's site.
      </label>
      {confirm.isError && !apiFieldError(confirm.error, "reference") && <p role="alert" className="text-body-sm text-terracotta">{confirm.error.message}</p>}
      <Button icon="task_alt" iconClassName="text-secondary-fixed" className="self-start" disabled={!attest || confirm.isPending || uploading} onClick={submit}>
        {uploading ? "Uploading…" : "Record submission"}
      </Button>
    </div>
  );
}

function FailForm({ app, attemptId }: { app: AppDetail; attemptId: string }) {
  const { fail } = useApplicationActions(app.id);
  const [reason, setReason] = useState("VERIFICATION_REQUIRED");
  const [message, setMessage] = useState("");
  return (
    <div className="flex flex-col gap-space-sm">
      <fieldset className="flex flex-col gap-1">
        <legend className="mb-1 text-[12px] font-medium text-on-surface-variant">What happened?</legend>
        {failureReasons.map((r) => (
          <label key={r.value} className="flex items-start gap-space-xs text-body-md text-on-surface">
            <input type="radio" name="reason" value={r.value} checked={reason === r.value} onChange={() => setReason(r.value)} className="mt-1" />
            {r.label}
          </label>
        ))}
      </fieldset>
      <TextInput label="Details (optional)" value={message} maxLength={1000} onChange={(e) => setMessage(e.target.value)} />
      {fail.isError && <p role="alert" className="text-body-sm text-terracotta">{fail.error.message}</p>}
      <Button variant="destructive" className="self-start" disabled={fail.isPending} onClick={() => fail.mutate({ attemptId, reason, message })}>
        Record failed attempt
      </Button>
    </div>
  );
}

/** Assisted submission (ADR-007): you submit on the official site, then record what happened. */
export function SubmissionPanel({ app }: { app: AppDetail }) {
  const start = useStartSubmission(app.id);
  const { cancel, revoke } = useApplicationActions(app.id);
  const [outcome, setOutcome] = useState<"confirm" | "fail" | null>(null);
  const pending = app.attempts.find((a) => a.status === "PENDING");

  return (
    <Panel className="flex flex-col gap-space-md p-space-md">
      <div>
        <p className="label-caps text-outline">Assisted submission</p>
        <h2 className="font-serif text-headline-sm text-on-surface">Submit on the official site</h2>
      </div>
      <p className="text-body-md text-on-surface-variant">
        CareerPilot does not fill in or submit employer forms, so CAPTCHAs, logins and other checks stay with you. Download your approved package,
        apply on the official page, then record what happened. The application is marked submitted only when you confirm it.
      </p>
      {!pending ? (
        <div className="flex flex-col gap-space-sm">
          {start.isError && <p role="alert" className="text-body-sm text-terracotta">{start.error.message}</p>}
          <div className="flex flex-wrap gap-space-sm">
            <Button icon="play_arrow" iconClassName="text-secondary-fixed" disabled={start.isPending} onClick={() => start.mutate(newIdempotencyKey())}>
              Start submission
            </Button>
            <Button variant="ghost" disabled={revoke.isPending} onClick={() => revoke.mutate()}>
              Revoke approval
            </Button>
          </div>
        </div>
      ) : (
        <ol className="flex flex-col gap-space-md">
          <li className="flex flex-col gap-1">
            <span className="font-semibold text-on-surface">1. Download your approved documents</span>
            <PackageDownloads app={app} />
          </li>
          <li className="flex flex-col gap-1">
            <span className="font-semibold text-on-surface">2. Apply on the employer's site</span>
            <a href={app.job.apply_url} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 self-start underline">
              <Icon name="open_in_new" size={16} /> Open the official application page
            </a>
          </li>
          <li className="flex flex-col gap-space-sm">
            <span className="font-semibold text-on-surface">3. Record the outcome</span>
            <div role="tablist" className="flex gap-space-sm">
              <Button variant={outcome === "confirm" ? "primary" : "secondary"} onClick={() => setOutcome("confirm")}>
                I submitted it
              </Button>
              <Button variant={outcome === "fail" ? "primary" : "secondary"} onClick={() => setOutcome("fail")}>
                It didn't go through
              </Button>
              <Button variant="ghost" disabled={cancel.isPending} onClick={() => cancel.mutate(pending.id)}>
                Cancel attempt
              </Button>
            </div>
            {outcome === "confirm" && <ConfirmForm app={app} attemptId={pending.id} />}
            {outcome === "fail" && <FailForm app={app} attemptId={pending.id} />}
          </li>
        </ol>
      )}
    </Panel>
  );
}
