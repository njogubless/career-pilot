import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { Button } from "../../components/ui/Button";
import { TextArea } from "../../components/ui/Form";
import { Icon } from "../../components/ui/Icon";
import { Loading } from "../../components/ui/Loading";
import { Panel } from "../../components/ui/Panel";
import { Tag } from "../../components/ui/Tag";
import { ApiError } from "../../lib/api";
import {
  nextStep,
  statusLabel,
  statusTone,
  useApplication,
  useApplicationActions,
  useDeleteApplication,
  type AppDetail,
  type AppStatus,
  type Channel,
} from "../../lib/applications";
import { relativeTime } from "../../lib/format";
import { kindLabel } from "../../lib/preparation";
import { SubmissionPanel } from "./SubmissionPanel";

function PackagePanel({ app }: { app: AppDetail }) {
  const p = app.package;
  return (
    <Panel>
      <div className="flex items-center justify-between border-b border-stone p-space-md">
        <div>
          <p className="label-caps text-outline">Application package</p>
          <h2 className="font-serif text-headline-sm text-on-surface">Documents</h2>
        </div>
        <Tag tone={p.ready ? "olive" : "outline"}>{p.ready ? "Ready for approval" : "Not ready"}</Tag>
      </div>
      {p.documents.length === 0 ? (
        <p className="p-space-md text-body-md text-on-surface-variant">
          No tailored documents yet.{" "}
          <Link to={`/jobs/${app.job.id}`} className="font-semibold underline">
            Prepare them on the job page
          </Link>
          .
        </p>
      ) : (
        <ul className="divide-y divide-stone">
          {p.documents.map((d) => (
            <li key={d.document_id} className="flex flex-wrap items-center justify-between gap-space-sm px-space-md py-space-sm">
              <Link to={`/prepare/${d.document_id}`} className="font-semibold text-on-surface hover:underline">
                {kindLabel[d.kind]}
              </Link>
              <span className="font-mono text-label-sm text-on-surface-variant">
                {d.approval_state === "NONE" ? "Not approved" : `Approved v${d.approved_version_no}`}
                {d.approval_state === "OUTDATED" ? ` · edited to v${d.current_version_no}` : ""}
              </span>
            </li>
          ))}
        </ul>
      )}
      {(p.blockers.length > 0 || p.warnings.length > 0) && (
        <div className="flex flex-col gap-1 border-t border-stone p-space-md text-body-sm">
          {p.blockers.map((b) => (
            <p key={b} className="text-terracotta">
              {b}
            </p>
          ))}
          {p.warnings.map((w) => (
            <p key={w} className="text-olive">
              {w}
            </p>
          ))}
        </div>
      )}
    </Panel>
  );
}

function ApprovalGate({ app }: { app: AppDetail }) {
  const { approve, transition } = useApplicationActions(app.id);
  const [channel, setChannel] = useState<Channel>("ASSISTED");
  const [confirm, setConfirm] = useState(false);
  return (
    <Panel className="flex flex-col gap-space-md p-space-md">
      <div>
        <p className="label-caps text-outline">Gatekeeper approval</p>
        <h2 className="font-serif text-headline-sm text-on-surface">Approve this application</h2>
      </div>
      <p className="text-body-md text-on-surface-variant">
        Approving pins the approved version of each document shown above and authorises submission through the channel you choose. Later edits to
        the documents are not included unless you approve again.
      </p>
      <fieldset className="flex flex-col gap-space-xs">
        <legend className="mb-1 text-[12px] font-medium text-on-surface-variant">Submission channel</legend>
        {app.channels.map((c) => (
          <label key={c.channel} className="flex items-start gap-space-xs text-body-md text-on-surface">
            <input type="radio" name="channel" disabled={!c.available} checked={channel === c.channel} onChange={() => setChannel(c.channel)} className="mt-1" />
            <span>
              {c.channel === "ASSISTED" ? "Assisted: I submit on the employer's official site" : "Email: send from my connected Gmail"}
              {!c.available && c.detail && <span className="block text-body-sm text-muted">{c.detail}</span>}
            </span>
          </label>
        ))}
      </fieldset>
      <label className="flex items-start gap-space-xs text-body-md text-on-surface">
        <input type="checkbox" checked={confirm} onChange={(e) => setConfirm(e.target.checked)} className="mt-1" />
        I have reviewed these documents and authorise this application to be submitted.
      </label>
      {approve.isError && <p role="alert" className="text-body-sm text-terracotta">{approve.error.message}</p>}
      <div className="flex flex-wrap gap-space-sm">
        <Button icon="verified" iconClassName="text-secondary-fixed" disabled={!confirm || !app.package.ready || approve.isPending} onClick={() => approve.mutate({ channel, confirm })}>
          Approve application
        </Button>
        <Button variant="ghost" disabled={transition.isPending} onClick={() => transition.mutate({ to: "PREPARING" })}>
          Back to preparing
        </Button>
      </div>
    </Panel>
  );
}

function FailedPanel({ app }: { app: AppDetail }) {
  const { transition, revoke } = useApplicationActions(app.id);
  const last = app.attempts.find((a) => a.status === "FAILED");
  return (
    <Panel className="flex flex-col gap-space-sm border-terracotta p-space-md">
      <p className="label-caps text-terracotta">Submission failed</p>
      <p className="text-body-md text-on-surface">{last?.error_message ?? "The last submission attempt did not go through."}</p>
      {last?.failure_reason === "VERIFICATION_REQUIRED" && (
        <p className="text-body-sm text-on-surface-variant">
          CareerPilot never bypasses CAPTCHAs, logins or other verification. Complete the step yourself on the employer's site, then retry.
        </p>
      )}
      <div className="flex flex-wrap gap-space-sm">
        <Button disabled={transition.isPending} onClick={() => transition.mutate({ to: "APPROVED", reason: "Retrying submission" })}>
          Retry submission
        </Button>
        <Button variant="secondary" disabled={transition.isPending} onClick={() => transition.mutate({ to: "PREPARING" })}>
          Reopen and edit
        </Button>
        <Button variant="ghost" disabled={revoke.isPending} onClick={() => revoke.mutate()}>
          Revoke approval
        </Button>
      </div>
    </Panel>
  );
}

/** Plain status changes the state machine allows from here. */
function StatusControls({ app }: { app: AppDetail }) {
  const { transition } = useApplicationActions(app.id);
  // Actions with their own panels are not repeated here.
  const handled = new Set<AppStatus>(app.status === "FAILED" ? ["APPROVED", "PREPARING"] : app.status === "AWAITING_APPROVAL" ? ["PREPARING"] : []);
  const targets = app.allowed_transitions.filter((t) => !handled.has(t));
  if (targets.length === 0) return <p className="text-body-sm text-muted">This application is closed.</p>;
  return (
    <div className="flex flex-col gap-space-xs">
      <div className="flex flex-wrap gap-space-xs">
        {targets.map((t) => (
          <Button
            key={t}
            variant={t === "WITHDRAWN" ? "destructive" : "secondary"}
            className="px-space-sm py-1 text-[13px]"
            disabled={transition.isPending}
            onClick={() => {
              if (t === "WITHDRAWN" && !window.confirm("Withdraw this application? Any submission in progress is cancelled.")) return;
              transition.mutate({ to: t });
            }}
          >
            {t === "AWAITING_APPROVAL" ? "Request approval" : `Mark ${statusLabel[t].toLowerCase()}`}
          </Button>
        ))}
      </div>
      {transition.isError && <p role="alert" className="text-body-sm text-terracotta">{transition.error.message}</p>}
    </div>
  );
}

type Event = { at: string; icon: string; title: string; detail?: string; tone?: "error" | "ok" };

function Timeline({ app }: { app: AppDetail }) {
  const events: Event[] = [
    ...app.history.map((h) => ({
      at: h.created_at,
      icon: "swap_horiz",
      title: h.from_status ? `${statusLabel[h.from_status]} → ${statusLabel[h.to_status]}` : `Created as ${statusLabel[h.to_status]}`,
      detail: [h.actor.toLowerCase(), h.reason].filter(Boolean).join(" · "),
    })),
    ...app.attempts.map((a) => ({
      at: a.finished_at ?? a.started_at,
      icon: a.status === "SUCCEEDED" ? "task_alt" : a.status === "FAILED" ? "error" : a.status === "CANCELLED" ? "block" : "pending",
      title: `Submission attempt ${a.status.toLowerCase()} (${a.channel.toLowerCase()})`,
      detail: [a.failure_reason?.toLowerCase().replace("_", " "), a.error_message].filter(Boolean).join(" · "),
      tone: a.status === "FAILED" ? ("error" as const) : a.status === "SUCCEEDED" ? ("ok" as const) : undefined,
    })),
    ...app.evidence.map((e) => ({
      at: e.created_at,
      icon: "verified",
      title: `Evidence recorded: ${e.type === "USER_ATTESTED" ? "your confirmation" : e.type.toLowerCase().replace("_", " ")}`,
      detail: [e.reference, e.note, e.document_id ? "attachment" : ""].filter(Boolean).join(" · "),
      tone: "ok" as const,
    })),
  ].sort((a, b) => b.at.localeCompare(a.at));
  return (
    <Panel>
      <div className="border-b border-stone p-space-md">
        <p className="label-caps text-outline">Execution history</p>
        <h2 className="font-serif text-headline-sm text-on-surface">Timeline</h2>
      </div>
      <ol className="divide-y divide-stone">
        {events.map((e, i) => (
          <li key={i} className="flex items-start gap-space-sm px-space-md py-space-sm">
            <Icon name={e.icon} size={18} className={e.tone === "error" ? "text-terracotta" : e.tone === "ok" ? "text-olive" : "text-outline"} />
            <div className="flex min-w-0 flex-1 flex-col">
              <span className="text-body-md text-on-surface">{e.title}</span>
              {e.detail && <span className="text-body-sm text-on-surface-variant">{e.detail}</span>}
            </div>
            <time className="font-mono text-label-sm text-muted" dateTime={e.at}>
              {relativeTime(e.at)}
            </time>
          </li>
        ))}
      </ol>
    </Panel>
  );
}

function NotesPanel({ app }: { app: AppDetail }) {
  const { notes } = useApplicationActions(app.id);
  const [text, setText] = useState(app.notes);
  return (
    <Panel className="flex flex-col gap-space-sm p-space-md">
      <TextArea label="Private notes" rows={4} maxLength={5000} value={text} onChange={(e) => setText(e.target.value)} />
      {notes.isError && <p className="text-body-sm text-terracotta">{notes.error.message}</p>}
      <Button variant="secondary" className="self-start" disabled={notes.isPending || text === app.notes} onClick={() => notes.mutate(text)}>
        Save notes
      </Button>
    </Panel>
  );
}

export function ApplicationDetailPage() {
  const { id = "" } = useParams();
  const app = useApplication(id);
  const navigate = useNavigate();
  const del = useDeleteApplication(id);

  if (app.isPending) return <Loading label="Loading application" />;
  if (app.isError) {
    return (
      <Panel className="p-space-lg">
        <p role="alert" className="text-terracotta">
          {app.error instanceof ApiError && app.error.status === 404 ? "This application does not exist." : app.error.message}
        </p>
        <Link to="/applications" className="mt-space-sm inline-block underline">
          Back to applications
        </Link>
      </Panel>
    );
  }
  const a = app.data;
  const submitted = a.evidence.length > 0;

  return (
    <div className="flex flex-col gap-space-lg">
      <nav aria-label="Breadcrumb" className="font-mono text-label-md text-muted">
        <Link to="/applications" className="hover:underline">
          Applications
        </Link>{" "}
        / <span className="text-on-surface">{a.job.company}</span>
      </nav>
      <header className="flex flex-col gap-space-xs">
        <div className="flex flex-wrap items-center gap-1">
          <Tag tone={statusTone[a.status]}>{statusLabel[a.status]}</Tag>
          {a.channel && <Tag tone="outline">{a.channel === "ASSISTED" ? "Assisted submission" : "Email"}</Tag>}
          {a.job.expired_at && <Tag tone="terracotta">Posting closed {relativeTime(a.job.expired_at)}</Tag>}
          {a.automation.paused && <Tag tone="terracotta">Automation paused</Tag>}
        </div>
        <h1 className="font-serif text-headline-lg-mobile text-on-surface md:text-headline-lg">{a.job.title}</h1>
        <p className="text-body-md text-on-surface-variant">
          {a.job.company} · {a.job.location || "Location not stated"} ·{" "}
          <Link to={`/jobs/${a.job.id}`} className="underline">
            Job dossier
          </Link>
        </p>
        <p className="flex items-center gap-1 text-body-md text-on-surface">
          <Icon name="arrow_forward" size={16} className="text-olive" /> Next: {nextStep(a)}
        </p>
      </header>

      <div className="grid grid-cols-1 items-start gap-space-lg lg:grid-cols-12">
        <div className="flex min-w-0 flex-col gap-space-lg lg:col-span-8">
          {a.status === "AWAITING_APPROVAL" && <ApprovalGate app={a} />}
          {a.status === "APPROVED" && <SubmissionPanel app={a} />}
          {a.status === "FAILED" && <FailedPanel app={a} />}
          {submitted && (
            <Panel className="flex flex-col gap-1 p-space-md">
              <p className="label-caps text-olive">Submitted {a.submitted_at ? relativeTime(a.submitted_at) : ""}</p>
              {a.evidence.map((e) => (
                <p key={e.id} className="text-body-md text-on-surface">
                  {e.type === "USER_ATTESTED" ? "Confirmed by you" : e.type}
                  {e.reference ? `: ${e.reference}` : ""}
                </p>
              ))}
            </Panel>
          )}
          <PackagePanel app={a} />
          <Timeline app={a} />
        </div>
        <aside className="flex min-w-0 flex-col gap-space-lg lg:col-span-4">
          <Panel className="flex flex-col gap-space-sm p-space-md">
            <p className="label-caps text-outline">Status</p>
            <StatusControls app={a} />
          </Panel>
          <NotesPanel key={a.notes} app={a} />
          {!a.submitted_at && (
            <Panel className="flex flex-col gap-space-xs p-space-md">
              {del.isError && <p className="text-body-sm text-terracotta">{del.error.message}</p>}
              <Button
                variant="destructive"
                className="self-start"
                disabled={del.isPending}
                onClick={() => {
                  if (window.confirm("Stop tracking this application? Its history is deleted; your documents are kept.")) {
                    del.mutate(undefined, { onSuccess: () => navigate("/applications") });
                  }
                }}
              >
                Stop tracking
              </Button>
            </Panel>
          )}
        </aside>
      </div>
    </div>
  );
}
