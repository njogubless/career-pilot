import { Link } from "react-router";
import { Loading } from "../../components/ui/Loading";
import { EmptyState, Panel } from "../../components/ui/Panel";
import { Tag } from "../../components/ui/Tag";
import { useAutomation } from "../../lib/applications";
import { relativeTime } from "../../lib/format";
import { statusLabel, statusTone, usePendingApprovals, useWorkflows } from "../../lib/workflows";
import { ObjectiveComposer } from "./ObjectiveComposer";

/** AI Intelligence Centre: objectives, plans, agent runs and approval requests. */
export function IntelligencePage() {
  const wfs = useWorkflows();
  const approvals = usePendingApprovals();
  const automation = useAutomation();
  return (
    <div className="flex flex-col gap-space-lg">
      <header className="flex max-w-3xl flex-col gap-space-xs">
        <p className="label-caps text-outline">AI intelligence</p>
        <h1 className="font-serif text-headline-lg-mobile text-on-surface md:text-display-lg">Intelligence Centre</h1>
        <p className="text-body-lg text-on-surface-variant">
          Give the agents an objective. A validated plan runs step by step, each step recorded, and it stops for your approval before anything is
          approved on your behalf.
        </p>
        {automation.data?.paused && (
          <p className="border-l-2 border-terracotta pl-space-sm text-body-sm text-on-surface">
            Automation is paused, so workflows wait.{" "}
            <Link to="/automation" className="font-semibold underline">
              Automation Centre
            </Link>
          </p>
        )}
      </header>

      {(approvals.data?.data.length ?? 0) > 0 && (
        <Panel className="border-graphite/40">
          <div className="border-b border-stone p-space-md">
            <p className="label-caps text-outline">Approval requests</p>
            <h2 className="font-serif text-headline-sm text-on-surface">Waiting for you</h2>
          </div>
          <ul className="divide-y divide-stone">
            {approvals.data!.data.map((a) => (
              <li key={a.id}>
                <Link to={`/intelligence/workflows/${a.workflow_id}`} className="flex flex-wrap items-center justify-between gap-space-sm px-space-md py-space-sm hover:bg-surface-container-low">
                  <span className="text-body-md text-on-surface">{a.summary}</span>
                  <span className="font-mono text-label-sm text-muted">expires {relativeTime(a.expires_at)}</span>
                </Link>
              </li>
            ))}
          </ul>
        </Panel>
      )}

      <ObjectiveComposer />

      <Panel>
        <div className="border-b border-stone p-space-md">
          <p className="label-caps text-outline">Workflow history</p>
          <h2 className="font-serif text-headline-sm text-on-surface">Runs</h2>
        </div>
        {wfs.isPending && <Loading />}
        {wfs.data?.data.length === 0 && (
          <EmptyState icon="psychology" title="No workflows yet">
            Describe an objective above to start your first run.
          </EmptyState>
        )}
        <ul className="divide-y divide-stone">
          {wfs.data?.data.map((w) => (
            <li key={w.id}>
              <Link to={`/intelligence/workflows/${w.id}`} className="flex flex-wrap items-center justify-between gap-space-sm px-space-md py-space-sm hover:bg-surface-container-low">
                <div className="flex min-w-0 flex-col">
                  <span className="truncate text-body-md text-on-surface">{w.objective || (w.template === "DISCOVER_AND_RANK" ? "Find and shortlist jobs" : "Find, prepare and approve")}</span>
                  <span className="font-mono text-label-sm text-muted">
                    {w.tasks_done}/{w.tasks_total} steps · started {relativeTime(w.created_at)}
                    {w.interpretation === "AI" ? " · AI-interpreted" : ""}
                  </span>
                </div>
                <div className="flex items-center gap-space-sm">
                  {w.pending_approvals > 0 && <Tag tone="citron">Approval needed</Tag>}
                  <Tag tone={statusTone[w.status]}>{statusLabel[w.status]}</Tag>
                </div>
              </Link>
            </li>
          ))}
        </ul>
      </Panel>
    </div>
  );
}
