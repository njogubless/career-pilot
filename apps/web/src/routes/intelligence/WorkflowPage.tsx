import clsx from "clsx";
import { useState } from "react";
import { Link, useParams } from "react-router";
import { Button } from "../../components/ui/Button";
import { Icon } from "../../components/ui/Icon";
import { Loading } from "../../components/ui/Loading";
import { Panel } from "../../components/ui/Panel";
import { Tag } from "../../components/ui/Tag";
import { ApiError } from "../../lib/api";
import { relativeTime } from "../../lib/format";
import {
  agentTitle,
  statusLabel,
  statusTone,
  useDecide,
  useWorkflow,
  useWorkflowControl,
  type Approval,
  type Task,
  type WorkflowDetail,
} from "../../lib/workflows";

const taskTone: Record<Task["status"], string> = {
  PENDING: "text-outline",
  READY: "text-outline",
  RUNNING: "text-on-surface",
  WAITING_APPROVAL: "text-on-surface",
  NEEDS_INPUT: "text-terracotta",
  SUCCEEDED: "text-olive",
  FAILED: "text-terracotta",
  SKIPPED: "text-muted",
  CANCELLED: "text-muted",
};
const taskIcon: Record<Task["status"], string> = {
  PENDING: "radio_button_unchecked",
  READY: "schedule",
  RUNNING: "progress_activity",
  WAITING_APPROVAL: "front_hand",
  NEEDS_INPUT: "edit_note",
  SUCCEEDED: "check_circle",
  FAILED: "error",
  SKIPPED: "remove_circle_outline",
  CANCELLED: "block",
};

function ApprovalPanel({ wf, a }: { wf: WorkflowDetail; a: Approval }) {
  const decide = useDecide(wf.id);
  const okIds = a.payload.items.filter((i) => i.ok).map((i) => i.job_id);
  const [selected, setSelected] = useState<string[]>(okIds);
  const toggle = (id: string) => setSelected(selected.includes(id) ? selected.filter((x) => x !== id) : [...selected, id]);
  return (
    <Panel className="border-graphite/40">
      <div className="border-b border-stone p-space-md">
        <p className="label-caps text-outline">Approval checkpoint</p>
        <h2 className="font-serif text-headline-sm text-on-surface">{a.summary}</h2>
        <p className="mt-1 text-body-sm text-on-surface-variant">{a.payload.note}</p>
      </div>
      <ul className="divide-y divide-stone">
        {a.payload.items.map((it) => (
          <li key={it.job_id} className="flex items-start gap-space-sm px-space-md py-space-sm">
            <input
              type="checkbox"
              aria-label={`Approve ${it.title}`}
              disabled={!it.ok}
              checked={selected.includes(it.job_id)}
              onChange={() => toggle(it.job_id)}
              className="mt-1"
            />
            <div className="flex min-w-0 flex-1 flex-col gap-0.5">
              <span className="text-body-md text-on-surface">
                {it.title} · {it.company}
              </span>
              <span className="font-mono text-label-sm text-on-surface-variant">
                Match {Math.round(it.score)}
                {it.validation ? ` · CV fact check: ${it.validation.toLowerCase().replace("_", " ")}` : ""}
              </span>
              {it.flags?.map((f) => (
                <span key={f} className="text-body-sm text-terracotta">
                  {f}
                </span>
              ))}
              <span className="flex gap-space-sm text-body-sm">
                {it.document_id && (
                  <Link to={`/prepare/${it.document_id}`} className="underline">
                    Review CV
                  </Link>
                )}
                <Link to={`/jobs/${it.job_id}`} className="underline">
                  Job
                </Link>
              </span>
            </div>
          </li>
        ))}
      </ul>
      <div className="flex flex-wrap items-center gap-space-sm border-t border-stone p-space-md">
        <Button
          icon="verified"
          iconClassName="text-secondary-fixed"
          disabled={selected.length === 0 || decide.isPending}
          onClick={() => decide.mutate({ id: a.id, decision: "APPROVE", approved_job_ids: selected, payload_hash: a.payload_hash })}
        >
          Approve {selected.length} selected
        </Button>
        <Button variant="ghost" disabled={decide.isPending} onClick={() => decide.mutate({ id: a.id, decision: "REJECT", approved_job_ids: [], payload_hash: a.payload_hash })}>
          Reject all
        </Button>
        <span className="font-mono text-label-sm text-muted">Expires {relativeTime(a.expires_at)}</span>
        {decide.isError && <p role="alert" className="w-full text-body-sm text-terracotta">{decide.error.message}</p>}
      </div>
    </Panel>
  );
}

function Controls({ wf }: { wf: WorkflowDetail }) {
  const c = useWorkflowControl(wf.id);
  const err = [c.cancel, c.pause, c.resume, c.retry].find((m) => m.isError)?.error;
  const active = ["PENDING", "RUNNING", "NEEDS_INPUT", "AWAITING_APPROVAL", "PAUSED"].includes(wf.status);
  return (
    <div className="flex flex-wrap items-center gap-space-sm">
      {(wf.status === "RUNNING" || wf.status === "AWAITING_APPROVAL" || wf.status === "PENDING") && (
        <Button variant="secondary" icon="pause" disabled={c.pause.isPending} onClick={() => c.pause.mutate()}>
          Pause
        </Button>
      )}
      {(wf.status === "PAUSED" || wf.status === "NEEDS_INPUT") && (
        <Button icon="play_arrow" disabled={c.resume.isPending} onClick={() => c.resume.mutate()}>
          Resume
        </Button>
      )}
      {(wf.status === "FAILED" || wf.status === "PARTIALLY_SUCCEEDED") && (
        <Button icon="replay" disabled={c.retry.isPending} onClick={() => c.retry.mutate()}>
          Retry failed steps
        </Button>
      )}
      {active && (
        <Button
          variant="destructive"
          disabled={c.cancel.isPending}
          onClick={() => {
            if (window.confirm("Cancel this workflow? Steps already completed are kept.")) c.cancel.mutate();
          }}
        >
          Cancel
        </Button>
      )}
      {err && <p role="alert" className="w-full text-body-sm text-terracotta">{err.message}</p>}
    </div>
  );
}

export function WorkflowPage() {
  const { id = "" } = useParams();
  const q = useWorkflow(id);
  if (q.isPending) return <Loading label="Loading workflow" />;
  if (q.isError) {
    return (
      <Panel className="p-space-lg">
        <p role="alert" className="text-terracotta">
          {q.error instanceof ApiError && q.error.status === 404 ? "This workflow does not exist." : q.error.message}
        </p>
      </Panel>
    );
  }
  const wf = q.data;
  const pending = wf.approvals.find((a) => a.status === "PENDING");

  return (
    <div className="flex flex-col gap-space-lg">
      <nav aria-label="Breadcrumb" className="font-mono text-label-md text-muted">
        <Link to="/intelligence" className="hover:underline">
          Intelligence Centre
        </Link>{" "}
        / <span className="text-on-surface">Workflow</span>
      </nav>
      <header className="flex flex-col gap-space-sm lg:flex-row lg:items-end lg:justify-between">
        <div className="flex max-w-3xl flex-col gap-space-xs">
          <div className="flex flex-wrap items-center gap-1">
            <Tag tone={statusTone[wf.status]}>{statusLabel[wf.status]}</Tag>
            <Tag tone="outline">{wf.template === "DISCOVER_AND_RANK" ? "Find and shortlist" : "Find, prepare and approve"}</Tag>
            <Tag tone="outline">
              {wf.interpretation === "AI" ? "AI-interpreted" : "Structured"} · {wf.plan_version}
            </Tag>
          </div>
          <h1 className="font-serif text-headline-lg-mobile text-on-surface md:text-headline-lg">{wf.objective || "Workflow"}</h1>
          {wf.status_reason && <p className="text-body-md text-on-surface-variant">{wf.status_reason}</p>}
        </div>
        <Controls wf={wf} />
      </header>

      {wf.status === "NEEDS_INPUT" && (
        <Panel className="flex flex-col gap-space-xs border-terracotta p-space-md">
          <p className="label-caps text-terracotta">The agents need your input</p>
          {wf.questions.map((qn) => (
            <p key={qn} className="text-body-md text-on-surface">
              {qn}
            </p>
          ))}
          <Link to="/profile" className="self-start font-semibold underline">
            Open your profile
          </Link>
        </Panel>
      )}

      {pending && <ApprovalPanel key={pending.id} wf={wf} a={pending} />}

      {wf.report && (
        <Panel className="flex flex-col gap-space-xs p-space-md">
          <p className="label-caps text-outline">Execution report</p>
          <h2 className="font-serif text-headline-sm text-on-surface">{wf.report.headline}</h2>
          {wf.report.lines.map((l) => (
            <p key={l} className="text-body-md text-on-surface-variant">
              {l}
            </p>
          ))}
          {wf.report.next_steps.length > 0 && (
            <ul className="mt-space-xs list-disc pl-space-md text-body-md text-on-surface">
              {wf.report.next_steps.map((s) => (
                <li key={s}>{s}</li>
              ))}
            </ul>
          )}
          <Link to="/applications" className="self-start font-semibold underline">
            Open Applications
          </Link>
        </Panel>
      )}

      <div className="grid grid-cols-1 items-start gap-space-lg lg:grid-cols-12">
        <Panel className="lg:col-span-7">
          <div className="border-b border-stone p-space-md">
            <p className="label-caps text-outline">Plan</p>
            <h2 className="font-serif text-headline-sm text-on-surface">Steps and agents</h2>
          </div>
          <ol className="divide-y divide-stone">
            {wf.tasks.map((t) => (
              <li key={t.id} className="flex items-start gap-space-sm px-space-md py-space-sm">
                <Icon name={taskIcon[t.status]} size={18} className={clsx("mt-0.5", taskTone[t.status], t.status === "RUNNING" && "animate-spin")} />
                <div className="flex min-w-0 flex-1 flex-col">
                  <span className="text-body-md text-on-surface">
                    {t.seq}. {t.type.replaceAll("_", " ")}
                  </span>
                  <span className="font-mono text-label-sm text-on-surface-variant">
                    {agentTitle[t.agent]} · {t.tool}
                    {t.required_permissions.length > 0 ? ` · needs ${t.required_permissions.join(", ")}` : ""}
                    {t.attempts > 0 ? ` · attempt ${t.attempts}/${t.max_attempts}` : ""}
                  </span>
                  {t.error_message && <span className="text-body-sm text-terracotta">{t.error_message}</span>}
                </div>
                <span className="font-mono text-label-sm text-muted">{t.status.toLowerCase().replace("_", " ")}</span>
              </li>
            ))}
          </ol>
        </Panel>
        <Panel className="lg:col-span-5">
          <div className="border-b border-stone p-space-md">
            <p className="label-caps text-outline">Audit</p>
            <h2 className="font-serif text-headline-sm text-on-surface">Tool calls</h2>
          </div>
          {wf.tool_calls.length === 0 && <p className="p-space-md text-body-sm text-muted">No tool has run yet.</p>}
          <ol className="divide-y divide-stone">
            {wf.tool_calls.map((c) => (
              <li key={c.id} className="flex flex-col px-space-md py-space-xs">
                <span className="flex items-center justify-between gap-space-sm font-mono text-label-md">
                  <span className="text-on-surface">{c.tool}</span>
                  <span className={c.outcome === "SUCCEEDED" ? "text-olive" : c.outcome === "DENIED" || c.outcome === "FAILED" ? "text-terracotta" : "text-on-surface-variant"}>
                    {c.outcome.toLowerCase().replace("_", " ")}
                  </span>
                </span>
                <span className="font-mono text-label-sm text-muted">
                  {agentTitle[c.agent]} · {c.duration_ms} ms · {relativeTime(c.created_at)}
                  {Object.keys(c.summary).length > 0 ? ` · ${Object.entries(c.summary).map(([k, v]) => `${k} ${v}`).join(", ")}` : ""}
                </span>
                {c.error_message && <span className="text-body-sm text-terracotta">{c.error_message}</span>}
              </li>
            ))}
          </ol>
        </Panel>
      </div>
    </div>
  );
}
