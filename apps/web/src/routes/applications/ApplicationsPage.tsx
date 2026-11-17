import clsx from "clsx";
import { useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { Icon } from "../../components/ui/Icon";
import { Loading } from "../../components/ui/Loading";
import { EmptyState, Panel } from "../../components/ui/Panel";
import { Tag } from "../../components/ui/Tag";
import { relativeTime } from "../../lib/format";
import {
  columns,
  nextStep,
  statusLabel,
  statusTone,
  useApplicationActivity,
  useApplications,
  useApplicationStats,
  useAutomation,
  type Activity,
  type AppStats,
  type AppSummary,
} from "../../lib/applications";

type View = "kanban" | "table" | "history";

function Metric({ label, icon, value, note, highlight }: { label: string; icon: string; value: string; note: string; highlight?: boolean }) {
  return (
    <div className={clsx("flex flex-col gap-1 border-stone p-space-md", highlight && "border-t-2 border-t-citron")}>
      <div className="flex items-center justify-between">
        <span className="label-caps text-outline">{label}</span>
        <Icon name={icon} size={16} className="text-outline" />
      </div>
      <span className="font-mono text-data-metric text-on-surface">{value}</span>
      <span className="text-body-sm text-muted">{note}</span>
    </div>
  );
}

function MetricStrip({ s }: { s: AppStats }) {
  const rate = s.response_rate === null ? "—" : `${Math.round(s.response_rate * 100)}%`;
  const median = s.median_days_to_response === null ? "No responses recorded yet" : `Median first response: ${s.median_days_to_response.toFixed(1)} days`;
  return (
    <Panel className="grid grid-cols-2 divide-stone md:grid-cols-5 md:divide-x">
      <Metric label="Total active" icon="stacked_bar_chart" value={String(s.active)} note="Not rejected or withdrawn" />
      <Metric label="Awaiting approval" icon="front_hand" value={String(s.awaiting_approval)} note={s.awaiting_approval ? "Action required" : "Nothing waiting"} highlight={s.awaiting_approval > 0} />
      <Metric label="Interviewing" icon="forum" value={String(s.interviewing)} note="Assessments and interviews" />
      <Metric label="Offers" icon="workspace_premium" value={String(s.offers)} note="Recorded by you" />
      <Metric label="Response rate" icon="speed" value={rate} note={s.submitted ? `${s.submitted} submitted · ${median}` : "Measured once you have submissions"} />
    </Panel>
  );
}

function Card({ a }: { a: AppSummary }) {
  const attention = a.pending_attempt || a.status === "AWAITING_APPROVAL" || a.status === "FAILED";
  return (
    <Link
      to={`/applications/${a.id}`}
      className={clsx(
        "flex flex-col gap-space-xs rounded-sm border bg-surface-container-lowest p-space-md hover:border-graphite",
        attention ? "border-graphite/40" : "border-stone",
      )}
    >
      <div className="flex items-center justify-between gap-space-sm">
        <span className="label-caps truncate text-outline">{a.job.company}</span>
        {a.match && <Tag tone="citron">{Math.round(a.match.score)} match</Tag>}
      </div>
      <span className="font-serif text-headline-sm leading-tight text-on-surface">{a.job.title}</span>
      <span className="flex items-center gap-1 text-body-sm text-on-surface-variant">
        <Icon name="location_on" size={14} className="text-outline" /> {a.job.location || "Location not stated"}
      </span>
      <div className="flex flex-wrap items-center gap-1">
        <Tag tone={statusTone[a.status]}>{statusLabel[a.status]}</Tag>
        {a.job.expired_at && <Tag tone="terracotta">Posting closed</Tag>}
      </div>
      <div className="mt-1 flex items-center justify-between gap-space-sm border-t border-stone pt-space-xs font-mono text-label-sm text-on-surface-variant">
        <span>{nextStep(a)}</span>
        <Icon name="arrow_forward" size={14} />
      </div>
    </Link>
  );
}

function Kanban({ items }: { items: AppSummary[] }) {
  return (
    // The board scrolls horizontally inside its frame; the page never does.
    <div className="-mx-space-md overflow-x-auto px-space-md pb-space-sm">
      <div className="grid min-w-[1100px] grid-cols-6 gap-space-md">
        {columns.map((col) => {
          const cards = items.filter((a) => col.statuses.includes(a.status));
          return (
            <section key={col.key} aria-label={col.title} className="flex flex-col gap-space-sm rounded-sm bg-surface-container p-space-sm">
              <div className="flex items-center justify-between px-1">
                <h2 className="text-body-md font-semibold text-on-surface">{col.title}</h2>
                <span className="font-mono text-label-sm text-muted">{cards.length}</span>
              </div>
              {cards.map((a) => (
                <Card key={a.id} a={a} />
              ))}
              {cards.length === 0 && <p className="px-1 text-body-sm text-muted">None</p>}
            </section>
          );
        })}
      </div>
    </div>
  );
}

function Matrix({ items }: { items: AppSummary[] }) {
  return (
    <Panel className="overflow-x-auto">
      <table className="w-full min-w-[720px] text-left text-body-md">
        <thead className="border-b border-stone">
          <tr className="label-caps text-outline">
            <th className="p-space-sm">Role</th>
            <th className="p-space-sm">Status</th>
            <th className="p-space-sm">Channel</th>
            <th className="p-space-sm">Match</th>
            <th className="p-space-sm">Submitted</th>
            <th className="p-space-sm">Updated</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-stone">
          {items.map((a) => (
            <tr key={a.id}>
              <td className="p-space-sm">
                <Link to={`/applications/${a.id}`} className="font-semibold text-on-surface hover:underline">
                  {a.job.title}
                </Link>
                <div className="text-body-sm text-on-surface-variant">{a.job.company}</div>
              </td>
              <td className="p-space-sm">
                <Tag tone={statusTone[a.status]}>{statusLabel[a.status]}</Tag>
              </td>
              <td className="p-space-sm font-mono text-label-md">{a.channel === "ASSISTED" ? "Assisted" : a.channel === "EMAIL" ? "Email" : "—"}</td>
              <td className="p-space-sm font-mono text-label-md">{a.match ? Math.round(a.match.score) : "—"}</td>
              <td className="p-space-sm font-mono text-label-md">{a.submitted_at ? relativeTime(a.submitted_at) : "—"}</td>
              <td className="p-space-sm font-mono text-label-md">{relativeTime(a.updated_at)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </Panel>
  );
}

export function ActivityList({ items }: { items: Activity[] }) {
  if (items.length === 0) return <p className="p-space-md text-body-sm text-muted">No pipeline activity yet.</p>;
  return (
    <ol className="divide-y divide-stone">
      {items.map((e, i) => (
        <li key={i} className="flex flex-wrap items-start justify-between gap-space-sm px-space-md py-space-sm">
          <div className="flex min-w-0 flex-col">
            <Link to={`/applications/${e.application_id}`} className="truncate text-body-md text-on-surface hover:underline">
              {e.company} · {e.job_title}
            </Link>
            <span className="font-mono text-label-sm text-on-surface-variant">
              {e.from_status ? `${statusLabel[e.from_status]} → ` : ""}
              {statusLabel[e.to_status]} · {e.actor.toLowerCase()}
              {e.reason ? ` · ${e.reason}` : ""}
            </span>
          </div>
          <span className="font-mono text-label-sm text-muted">{relativeTime(e.created_at)}</span>
        </li>
      ))}
    </ol>
  );
}

const views: { key: View; label: string; icon: string }[] = [
  { key: "kanban", label: "Kanban pipeline", icon: "view_kanban" },
  { key: "table", label: "Matrix view", icon: "table_rows" },
  { key: "history", label: "Execution history", icon: "history_toggle_off" },
];

export function ApplicationsPage() {
  const [params, setParams] = useSearchParams();
  const view = (views.find((v) => v.key === params.get("view"))?.key ?? "kanban") as View;
  const [filter, setFilter] = useState("");
  const apps = useApplications();
  const stats = useApplicationStats();
  const automation = useAutomation();
  const activity = useApplicationActivity(view === "history" ? 100 : 6);

  const items = useMemo(() => {
    const q = filter.trim().toLowerCase();
    const all = apps.data?.data ?? [];
    return q ? all.filter((a) => `${a.job.title} ${a.job.company}`.toLowerCase().includes(q)) : all;
  }, [apps.data, filter]);

  const mode = automation.data?.mode === "CONTROLLED" ? "Controlled autonomy" : automation.data?.mode === "DISCOVERY_ONLY" ? "Discovery only" : "Human-approved";

  return (
    <div className="flex flex-col gap-space-lg">
      <div className="flex flex-wrap items-center gap-x-space-md gap-y-1 border-b border-stone pb-space-sm font-mono text-label-md text-on-surface-variant">
        <span className="flex items-center gap-1">
          <span className={clsx("h-2 w-2 rounded-full", automation.data?.paused ? "bg-terracotta" : "bg-citron")} aria-hidden />
          Pipeline telemetry
        </span>
        <span>
          Execution mode: <span className="text-on-surface">{mode}</span>
        </span>
        <span>
          Submission channel: <span className="text-on-surface">Assisted (you submit on the official site)</span>
        </span>
        {automation.data?.paused && (
          <Link to="/settings#automation" className="text-terracotta underline">
            Automation paused
          </Link>
        )}
      </div>

      <header className="flex flex-col gap-space-md lg:flex-row lg:items-end lg:justify-between">
        <div className="flex max-w-2xl flex-col gap-space-xs">
          <p className="label-caps text-outline">Dossier tracking</p>
          <h1 className="font-serif text-headline-lg-mobile text-on-surface md:text-display-lg">Applications</h1>
          <p className="text-body-lg text-on-surface-variant">
            Every application moves through an explicit pipeline. Nothing is submitted without your approval, and an application only counts as
            submitted once you record evidence.
          </p>
        </div>
        <div role="tablist" aria-label="View" className="flex rounded-sm border border-stone bg-surface-container-lowest p-0.5">
          {views.map((v) => (
            <button
              key={v.key}
              role="tab"
              aria-selected={view === v.key}
              onClick={() => setParams(v.key === "kanban" ? {} : { view: v.key })}
              className={clsx(
                "flex items-center gap-1 rounded-xs px-space-sm py-1.5 font-mono text-label-md",
                view === v.key ? "bg-surface-container text-on-surface" : "text-on-surface-variant hover:text-on-surface",
              )}
            >
              <Icon name={v.icon} size={16} /> {v.label}
            </button>
          ))}
        </div>
      </header>

      {stats.data && <MetricStrip s={stats.data} />}

      {apps.isPending && <Loading label="Loading applications" />}
      {apps.isError && (
        <p role="alert" className="text-terracotta">
          {apps.error.message}
        </p>
      )}
      {apps.data && apps.data.data.length === 0 && (
        <Panel>
          <EmptyState
            icon="view_kanban"
            title="No applications yet"
            action={
              <Link to="/discover" className="font-semibold underline">
                Discover jobs
              </Link>
            }
          >
            Open a job and choose “Track application” to add it to your pipeline. You then prepare documents, approve the package and submit on the
            employer’s official site.
          </EmptyState>
        </Panel>
      )}

      {apps.data && apps.data.data.length > 0 && view !== "history" && (
        <>
          <label className="flex items-center gap-space-sm rounded-sm border border-stone bg-surface-container-lowest px-space-sm py-1.5">
            <Icon name="filter_list" size={18} className="text-outline" />
            <input
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder="Filter by company or role"
              aria-label="Filter applications"
              className="w-full bg-transparent text-body-md text-on-surface placeholder:text-outline focus:outline-none"
            />
          </label>
          {view === "kanban" ? <Kanban items={items} /> : <Matrix items={items} />}
        </>
      )}

      {(view === "history" || (apps.data && apps.data.data.length > 0)) && (
        <Panel>
          <div className="flex items-center justify-between border-b border-stone p-space-md">
            <h2 className="flex items-center gap-space-xs font-serif text-headline-sm text-on-surface">
              <Icon name="history" size={20} /> {view === "history" ? "Execution history" : "Recent pipeline activity"}
            </h2>
            {view !== "history" && (
              <button onClick={() => setParams({ view: "history" })} className="font-mono text-label-md text-on-surface-variant underline">
                View all
              </button>
            )}
          </div>
          {activity.data && <ActivityList items={activity.data.data} />}
        </Panel>
      )}
    </div>
  );
}
