import { useState } from "react";
import { Link, useParams } from "react-router";
import { Button } from "../../components/ui/Button";
import { TextArea, TextInput } from "../../components/ui/Form";
import { Icon } from "../../components/ui/Icon";
import { Loading } from "../../components/ui/Loading";
import { Panel } from "../../components/ui/Panel";
import { Tag } from "../../components/ui/Tag";
import { ApiError, apiFieldError } from "../../lib/api";
import { relativeTime } from "../../lib/format";
import { messageStatusLabel, messageStatusTone, sendKey, useGmailStatus, useMessage, useMessageActions, type Message } from "../../lib/outreach";
import { useApplicationDocuments } from "../../lib/preparation";
import { GmailBanner } from "./OutreachPage";

function Composer({ m }: { m: Message }) {
  const { edit } = useMessageActions(m.id);
  const docs = useApplicationDocuments();
  const [subject, setSubject] = useState(m.subject);
  const [body, setBody] = useState(m.body);
  const [attach, setAttach] = useState(m.attachment_version_id ?? "");
  const cvs = (docs.data?.data ?? []).filter((d) => d.kind === "CV" && d.approved_version_id);
  const dirty = subject !== m.subject || body !== m.body || (attach || null) !== m.attachment_version_id;
  return (
    <Panel className="flex flex-col gap-space-sm p-space-md">
      <p className="font-mono text-label-md text-on-surface-variant">To: {m.contact_name ? `${m.contact_name} <${m.to_email}>` : m.to_email}</p>
      <TextInput label="Subject" value={subject} maxLength={200} disabled={m.kind === "FOLLOW_UP"} onChange={(e) => setSubject(e.target.value)} error={apiFieldError(edit.error, "subject")} />
      <TextArea label="Email" rows={14} value={body} maxLength={10000} onChange={(e) => setBody(e.target.value)} error={apiFieldError(edit.error, "body")} />
      <label className="flex flex-col gap-1 text-[12px] font-medium text-on-surface-variant">
        Attachment
        <select value={attach} onChange={(e) => setAttach(e.target.value)} className="rounded-sm border border-stone bg-surface-container-lowest px-2 py-1.5 text-body-md text-on-surface">
          <option value="">No attachment</option>
          {cvs.map((d) => (
            <option key={d.id} value={d.approved_version_id!}>
              Approved tailored CV, {d.company} · {d.job_title}
            </option>
          ))}
        </select>
      </label>
      {edit.isError && !apiFieldError(edit.error, "subject") && !apiFieldError(edit.error, "body") && <p className="text-body-sm text-terracotta">{edit.error.message}</p>}
      <div className="flex items-center gap-space-sm">
        <Button variant="secondary" disabled={!dirty || edit.isPending} onClick={() => edit.mutate({ subject, body, attachment_version_id: attach || null })}>
          Save changes
        </Button>
        {dirty && <span className="text-body-sm text-muted">Saving re-runs the fact check{m.status === "APPROVED" ? " and withdraws the approval" : ""}.</span>}
      </div>
    </Panel>
  );
}

function FactCheck({ m }: { m: Message }) {
  const v = m.validation;
  return (
    <Panel className="flex flex-col gap-space-xs p-space-md">
      <div className="flex items-center justify-between">
        <p className="label-caps text-outline">Fact check</p>
        <Tag tone={!v ? "outline" : v.status === "FAILED" ? "terracotta" : v.status === "WARNINGS" ? "stone" : "olive"}>
          {!v ? "Not checked" : v.status === "FAILED" ? "Unsupported statements" : v.status === "WARNINGS" ? "Warnings" : "Passed"}
        </Tag>
      </div>
      {!v && <p className="text-body-sm text-muted">The fact check could not run (AI service unavailable). Read the email carefully.</p>}
      {v?.issues.map((i, n) => (
        <p key={n} className={i.severity === "ERROR" ? "text-body-sm text-terracotta" : "text-body-sm text-olive"}>
          {i.message}
        </p>
      ))}
      {(m.questions?.length ?? 0) > 0 && (
        <div className="mt-space-xs">
          <p className="label-caps text-outline">Questions for you</p>
          <ul className="list-disc pl-space-md text-body-md">
            {m.questions!.map((q) => (
              <li key={q}>{q}</li>
            ))}
          </ul>
        </div>
      )}
      {m.warnings?.map((w) => (
        <p key={w} className="text-body-sm text-olive">
          {w}
        </p>
      ))}
    </Panel>
  );
}

function Actions({ m }: { m: Message }) {
  const a = useMessageActions(m.id);
  const gmail = useGmailStatus();
  const [confirm, setConfirm] = useState(false);
  const [ack, setAck] = useState(false);
  const errors = m.validation?.issues.filter((i) => i.severity === "ERROR").length ?? 0;
  const connected = gmail.data?.account?.status === "CONNECTED";
  const err = [a.approve, a.send, a.cancel, a.markNotSent].find((x) => x.isError)?.error;
  return (
    <Panel className="flex flex-col gap-space-sm p-space-md">
      <p className="label-caps text-outline">Approval and sending</p>
      {m.status === "DRAFT" && (
        <>
          <label className="flex items-start gap-space-xs text-body-md text-on-surface">
            <input type="checkbox" checked={confirm} onChange={(e) => setConfirm(e.target.checked)} className="mt-1" />I have read this email and want it sent
            from my Gmail to {m.to_email}.
          </label>
          {errors > 0 && (
            <label className="flex items-start gap-space-xs text-body-md text-on-surface">
              <input type="checkbox" checked={ack} onChange={(e) => setAck(e.target.checked)} className="mt-1" />I have checked the {errors} flagged statement(s)
              and they are true.
            </label>
          )}
          <Button icon="verified" iconClassName="text-secondary-fixed" className="self-start" disabled={!confirm || (errors > 0 && !ack) || a.approve.isPending} onClick={() => a.approve.mutate({ confirm, acknowledge_issues: ack })}>
            Approve email
          </Button>
        </>
      )}
      {m.status === "APPROVED" && (
        <>
          <p className="text-body-md text-on-surface-variant">Approved {m.approved_at ? relativeTime(m.approved_at) : ""}. Editing it withdraws the approval.</p>
          <Button icon="send" iconClassName="text-secondary-fixed" className="self-start" disabled={!connected || a.send.isPending} onClick={() => a.send.mutate(sendKey())}>
            {a.send.isPending ? "Sending…" : "Send now"}
          </Button>
          {!connected && <p className="text-body-sm text-muted">Connect Gmail in Settings to send.</p>}
        </>
      )}
      {m.status === "SENT" && <p className="text-body-md text-on-surface">Sent {m.sent_at ? relativeTime(m.sent_at) : ""} from your Gmail.</p>}
      {m.status === "FAILED" && <p className="text-body-md text-terracotta">Not sent: {m.error_message}</p>}
      {m.status === "UNKNOWN" && (
        <>
          <p className="text-body-md text-terracotta">{m.error_message}</p>
          <Button variant="secondary" className="self-start" disabled={a.markNotSent.isPending} onClick={() => a.markNotSent.mutate()}>
            I checked: it was not sent
          </Button>
        </>
      )}
      {(m.status === "DRAFT" || m.status === "APPROVED") && (
        <Button variant="ghost" className="self-start" disabled={a.cancel.isPending} onClick={() => a.cancel.mutate()}>
          Discard email
        </Button>
      )}
      {err && <p role="alert" className="text-body-sm text-terracotta">{err.message}</p>}
    </Panel>
  );
}

function Thread({ m }: { m: Message }) {
  if (m.status !== "SENT") return null;
  return (
    <Panel className="flex flex-col gap-space-xs p-space-md">
      <p className="label-caps text-outline">Replies and follow-ups</p>
      {(m.replies ?? []).length === 0 && <p className="text-body-sm text-muted">No reply detected yet. Replies are checked every 15 minutes (headers only).</p>}
      {m.replies?.map((r) => (
        <p key={r.id} className="flex items-center gap-1 text-body-md text-on-surface">
          <Icon name={r.is_bounce ? "report" : "reply"} size={16} className={r.is_bounce ? "text-terracotta" : "text-olive"} />
          {r.is_bounce ? "Bounced" : `Reply from ${r.from_address}`} · {relativeTime(r.received_at)}
        </p>
      ))}
      {m.follow_ups?.map((f) => (
        <p key={f.sequence} className="text-body-sm text-on-surface-variant">
          Follow-up {f.sequence}:{" "}
          {f.status === "SCHEDULED"
            ? `a draft will be prepared ${relativeTime(f.due_at)} if there's no reply`
            : f.status === "DRAFTED"
              ? <Link to={`/outreach/messages/${f.draft_id}`} className="underline">draft ready for review</Link>
              : `cancelled (${f.cancelled_reason})`}
        </p>
      ))}
    </Panel>
  );
}

export function MessagePage() {
  const { id = "" } = useParams();
  const msg = useMessage(id);
  if (msg.isPending) return <Loading label="Loading email" />;
  if (msg.isError) {
    return (
      <Panel className="p-space-lg">
        <p role="alert" className="text-terracotta">
          {msg.error instanceof ApiError && msg.error.status === 404 ? "This email does not exist." : msg.error.message}
        </p>
      </Panel>
    );
  }
  const m = msg.data;
  const editable = m.status === "DRAFT" || m.status === "APPROVED";
  return (
    <div className="flex flex-col gap-space-lg">
      <nav aria-label="Breadcrumb" className="font-mono text-label-md text-muted">
        <Link to="/outreach" className="hover:underline">
          Recruiter outreach
        </Link>{" "}
        / <span className="text-on-surface">{m.kind === "FOLLOW_UP" ? "Follow-up" : "Email"}</span>
      </nav>
      <header className="flex flex-col gap-space-xs">
        <div className="flex flex-wrap items-center gap-1">
          <Tag tone={messageStatusTone[m.status]}>{messageStatusLabel[m.status]}</Tag>
          <Tag tone="outline">{m.origin === "AI" ? "AI draft" : m.origin === "FOLLOW_UP_SCHEDULER" ? "Scheduled follow-up" : m.origin === "USER" ? "Edited by you" : "Template"}</Tag>
          {m.replied_at && <Tag tone="citron">Replied</Tag>}
        </div>
        <h1 className="font-serif text-headline-lg-mobile text-on-surface md:text-headline-lg">{m.subject}</h1>
        <div className="flex flex-wrap gap-space-sm text-body-md">
          {m.application_id && (
            <Link to={`/applications/${m.application_id}`} className="underline">
              Linked application
            </Link>
          )}
          {m.job_id && (
            <Link to={`/jobs/${m.job_id}`} className="underline">
              Job dossier
            </Link>
          )}
        </div>
        <GmailBanner />
      </header>
      <div className="grid grid-cols-1 items-start gap-space-lg lg:grid-cols-12">
        <div className="flex min-w-0 flex-col gap-space-lg lg:col-span-8">
          {editable ? (
            <Composer key={m.updated_at} m={m} />
          ) : (
            <Panel className="flex flex-col gap-space-sm p-space-md">
              <p className="font-mono text-label-md text-on-surface-variant">To: {m.to_email}</p>
              <p className="whitespace-pre-wrap text-body-md text-on-surface">{m.body}</p>
            </Panel>
          )}
          <Thread m={m} />
        </div>
        <aside className="flex min-w-0 flex-col gap-space-lg lg:col-span-4">
          <Actions m={m} />
          <FactCheck m={m} />
        </aside>
      </div>
    </div>
  );
}
