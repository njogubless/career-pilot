import { useState } from "react";
import { Button } from "../../components/ui/Button";
import { Panel } from "../../components/ui/Panel";
import { Tag } from "../../components/ui/Tag";
import { relativeTime } from "../../lib/format";
import {
  blockingCodes,
  originLabel,
  statusLabel,
  statusTone,
  useApprove,
  useRecheck,
  useRestoreVersion,
  useRevokeApproval,
  useVersions,
  type DocumentView,
  type Validation,
  type ValidationStatus,
  type Version,
} from "../../lib/preparation";

function counts(v: Validation | null) {
  const errors = v?.issues.filter((i) => i.severity === "ERROR") ?? [];
  return {
    errors: errors.length,
    blocking: errors.filter((i) => blockingCodes.has(i.code)).length,
    warnings: v?.issues.filter((i) => i.severity === "WARNING").length ?? 0,
  };
}

export function FactCheckPanel({
  docId,
  version,
  validation,
  live,
  onLive,
  dirty,
}: {
  docId: string;
  version: Version;
  validation: Validation | null;
  live: boolean;
  onLive: (v: Validation) => void;
  dirty: boolean;
}) {
  const recheck = useRecheck(docId);
  const status: ValidationStatus = validation?.status ?? "NOT_VALIDATED";
  const c = counts(validation);
  return (
    <Panel className="flex flex-col gap-space-sm p-space-md">
      <div className="flex items-center justify-between">
        <p className="label-caps text-outline">Factual validation</p>
        <Tag tone={statusTone[status]}>{statusLabel[status]}</Tag>
      </div>
      {validation ? (
        <p className="text-body-sm text-on-surface-variant">
          {validation.claims_checked} statements checked: {c.errors} {c.errors === 1 ? "error" : "errors"}, {c.warnings}{" "}
          {c.warnings === 1 ? "warning" : "warnings"}. {live ? "Checked against your current profile." : `Checked when version ${version.version_no} was saved.`}
        </p>
      ) : (
        <p className="text-body-sm text-on-surface-variant">The fact check could not run when this version was saved (the AI service was unavailable).</p>
      )}
      <p className="text-body-sm text-muted">
        The check flags figures, skills, years, degrees and certifications your verified profile does not support. It cannot prove free text is true: read
        every statement before approving.
      </p>
      {dirty && <p className="text-body-sm text-terracotta">You have unsaved changes. Save to check them.</p>}
      {version.facts_changed && !live && (
        <p className="text-body-sm text-terracotta">Your profile changed after this check. Re-check before approving.</p>
      )}
      {recheck.isError && <p className="text-body-sm text-terracotta">{recheck.error.message}</p>}
      <Button variant="secondary" icon="fact_check" className="self-start" disabled={recheck.isPending || dirty} onClick={() => recheck.mutate(undefined, { onSuccess: onLive })}>
        {recheck.isPending ? "Checking…" : "Re-check against current profile"}
      </Button>
    </Panel>
  );
}

export function QuestionsPanel({ version }: { version: Version }) {
  if (version.questions.length === 0 && version.warnings.length === 0) return null;
  return (
    <Panel className="flex flex-col gap-space-sm p-space-md">
      {version.questions.length > 0 && (
        <>
          <p className="label-caps text-outline">Questions for you</p>
          <p className="text-body-sm text-on-surface-variant">
            The draft needs information your profile does not contain. Add it to your profile (or to the notes when you regenerate) rather than guessing.
          </p>
          <ul className="flex list-disc flex-col gap-1 pl-space-md text-body-md text-on-surface">
            {version.questions.map((q) => (
              <li key={q}>{q}</li>
            ))}
          </ul>
        </>
      )}
      {version.warnings.map((w) => (
        <p key={w} className="text-body-sm text-olive">
          {w}
        </p>
      ))}
    </Panel>
  );
}

export function ApprovalPanel({ doc, version, validation, dirty }: { doc: DocumentView; version: Version; validation: Validation | null; dirty: boolean }) {
  const approve = useApprove(doc.id);
  const revoke = useRevokeApproval(doc.id);
  const [ack, setAck] = useState(false);
  const c = counts(validation);
  const approvedHere = doc.approval_state === "CURRENT";
  return (
    <Panel className="flex flex-col gap-space-sm p-space-md">
      <div className="flex items-center justify-between">
        <p className="label-caps text-outline">Approval</p>
        {approvedHere ? (
          <Tag tone="olive">Version {version.version_no} approved</Tag>
        ) : doc.approval_state === "OUTDATED" ? (
          <Tag tone="terracotta">An older version is approved</Tag>
        ) : (
          <Tag tone="outline">Not approved</Tag>
        )}
      </div>
      <p className="text-body-sm text-on-surface-variant">
        Approval confirms that this version is accurate and may be used for this application. Nothing is sent: submission arrives in Phase 6 and needs
        its own confirmation.
      </p>
      {approvedHere ? (
        <>
          {doc.approved_at && <p className="font-mono text-label-sm text-muted">Approved {relativeTime(doc.approved_at)}</p>}
          <Button variant="destructive" className="self-start" disabled={revoke.isPending} onClick={() => revoke.mutate()}>
            Revoke approval
          </Button>
        </>
      ) : (
        <>
          {c.blocking > 0 && (
            <p className="text-body-sm text-terracotta">{c.blocking} part(s) still need your input. Complete or remove them before approving.</p>
          )}
          {c.errors > c.blocking && (
            <label className="flex items-start gap-space-xs text-body-sm text-on-surface">
              <input type="checkbox" checked={ack} onChange={(e) => setAck(e.target.checked)} className="mt-1" />
              I have checked the {c.errors - c.blocking} flagged statement(s) and confirm they are true.
            </label>
          )}
          {approve.isError && <p role="alert" className="text-body-sm text-terracotta">{approve.error.message}</p>}
          <Button
            icon="verified"
            iconClassName="text-secondary-fixed"
            className="self-start"
            disabled={approve.isPending || dirty || c.blocking > 0 || (c.errors > 0 && !ack)}
            onClick={() => approve.mutate({ version_id: version.id, acknowledge_issues: ack })}
          >
            {approve.isPending ? "Checking and approving…" : `Approve version ${version.version_no}`}
          </Button>
          {dirty && <p className="text-body-sm text-muted">Save your changes first. Only saved versions can be approved.</p>}
        </>
      )}
    </Panel>
  );
}

export function HistoryPanel({
  docId,
  currentId,
  approvedId,
  viewing,
  onView,
  dirty,
}: {
  docId: string;
  currentId: string;
  approvedId: string | null;
  viewing: string | null;
  onView: (id: string | null) => void;
  dirty: boolean;
}) {
  const versions = useVersions(docId);
  const restore = useRestoreVersion(docId);
  return (
    <Panel>
      <div className="border-b border-stone p-space-md">
        <p className="label-caps text-outline">Version history</p>
        <p className="text-body-sm text-muted">Every draft, edit and restore is kept. Versions cannot be changed after they are saved.</p>
      </div>
      {versions.isError && <p className="p-space-md text-body-sm text-terracotta">{versions.error.message}</p>}
      {restore.isError && <p className="px-space-md pt-space-sm text-body-sm text-terracotta">{restore.error.message}</p>}
      <ol className="divide-y divide-stone">
        {versions.data?.data.map((v) => (
          <li key={v.id} className="flex flex-wrap items-center justify-between gap-space-xs px-space-md py-space-sm">
            <div className="flex flex-col">
              <span className="text-body-md text-on-surface">
                v{v.version_no} · {originLabel[v.origin]}
                {v.id === currentId && <span className="ml-1 font-mono text-label-sm text-olive">current</span>}
                {v.id === approvedId && <span className="ml-1 font-mono text-label-sm text-olive">approved</span>}
              </span>
              <span className="font-mono text-label-sm text-muted">
                {relativeTime(v.created_at)} · {statusLabel[v.validation_status]}
                {v.prompt_version ? ` · ${v.prompt_version}` : ""}
              </span>
            </div>
            {v.id !== currentId && (
              <div className="flex gap-space-xs">
                <Button variant="ghost" className="px-2 py-1 text-[13px]" onClick={() => onView(viewing === v.id ? null : v.id)}>
                  {viewing === v.id ? "Close" : "View"}
                </Button>
                <Button variant="ghost" className="px-2 py-1 text-[13px]" disabled={restore.isPending || dirty} onClick={() => restore.mutate(v.id, { onSuccess: () => onView(null) })}>
                  Restore
                </Button>
              </div>
            )}
          </li>
        ))}
      </ol>
    </Panel>
  );
}
