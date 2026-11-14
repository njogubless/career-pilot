import { Link } from "react-router";
import { EmptyState, Panel, SectionHeader } from "../components/ui/Panel";
import { Tag, type TagTone } from "../components/ui/Tag";
import { Icon } from "../components/ui/Icon";
import { Loading } from "../components/ui/Loading";
import { useMeta, type IntegrationStatus } from "../lib/meta";
import { useOverview } from "../lib/jobs";
import { useProfile } from "../lib/profile";
import { useSession } from "../lib/session";

/**
 * Overview, structured after the Stitch "Career Intelligence Terminal".
 * All values are live. Until discovery, matching and applications exist, the
 * metric tiles and opportunity feed show explicit empty states, never sample data.
 */

const metrics = [
  { label: "Discovered", icon: "radar" },
  { label: "High match (≥90%)", icon: "verified" },
  { label: "Active in-flight", icon: "send_and_archive" },
  { label: "Interviews", icon: "event_available" },
  { label: "Recruiter replies", icon: "mark_email_unread" },
  { label: "Verified submissions", icon: "task_alt" },
];

const integrationLabels: Record<string, string> = {
  ai_service: "AI service",
  document_storage: "Document storage",
  llm: "LLM provider",
  job_sources: "Job sources",
  gmail: "Gmail",
};

const statusTone: Record<IntegrationStatus, TagTone> = {
  available: "citron",
  unavailable: "terracotta",
  not_configured: "outline",
  not_implemented: "stone",
};

const statusText: Record<IntegrationStatus, string> = {
  available: "Available",
  unavailable: "Unavailable",
  not_configured: "Not configured",
  not_implemented: "Not built yet",
};

function greeting(): string {
  const h = new Date().getHours();
  return h < 12 ? "Good morning" : h < 18 ? "Good afternoon" : "Good evening";
}

/** Setup checklist driven by the server's profile completeness. */
function SetupChecklist() {
  const profile = useProfile();
  if (profile.isPending) return <Loading label="Checking your profile" />;
  if (profile.isError) return <p className="p-space-md text-body-sm text-terracotta">{profile.error.message}</p>;
  const c = profile.data.completeness;
  const hasCV = profile.data.experiences.length > 0 || profile.data.skills.length > 0;
  return (
    <>
      <div className="flex items-baseline justify-between px-space-md pb-space-sm">
        <span className="label-caps text-outline">Profile completeness</span>
        <span className="font-mono text-data-metric text-on-surface">{c.score}%</span>
      </div>
      <ol className="divide-y divide-stone border-t border-stone">
        {!hasCV && (
          <li className="flex items-center justify-between gap-space-md px-space-md py-space-sm">
            <Link to="/documents" className="text-title-md text-on-surface underline-offset-4 hover:underline">
              Upload your CV and review the extracted profile
            </Link>
            <Tag tone="citron">Start here</Tag>
          </li>
        )}
        {c.missing.map((m) => (
          <li key={m.key} className="flex items-center justify-between gap-space-md px-space-md py-space-sm">
            <Link to="/profile" className="min-w-0 text-title-md text-on-surface underline-offset-4 hover:underline">
              {m.label}
            </Link>
            <span className="font-mono text-label-sm text-muted">+{m.weight}%</span>
          </li>
        ))}
        <li className="flex items-center justify-between gap-space-md px-space-md py-space-sm">
          <Link to="/discover" className="text-title-md text-on-surface underline-offset-4 hover:underline">
            Browse discovered opportunities
          </Link>
          <Tag tone="outline">Match scores: Phase 4</Tag>
        </li>
      </ol>
    </>
  );
}

export function OverviewPage() {
  const meta = useMeta();
  const session = useSession();
  const overview = useOverview();
  const firstName = session.data?.display_name.split(/\s+/)[0];

  return (
    <div className="flex flex-col gap-space-lg">
      <Panel className="flex flex-col gap-space-xs p-space-lg">
        <div className="flex flex-wrap items-center gap-space-xs">
          <Tag tone="graphite">Portfolio overview</Tag>
          <span className="label-caps text-outline">Human-approved mode</span>
        </div>
        <h1 className="mt-1 font-serif text-headline-lg-mobile tracking-tight text-on-surface md:text-headline-lg">
          {firstName ? `${greeting()}, ${firstName}.` : "Career execution, under your control."}
        </h1>
        <p className="max-w-2xl text-body-lg text-on-surface-variant">
          CareerPilot discovers relevant roles, explains how well they fit, prepares truthful applications and asks for your
          approval before anything leaves the platform. Your workspace fills in as you complete each step below.
        </p>
      </Panel>

      <section aria-label="Key metrics" className="grid grid-cols-2 gap-space-sm md:grid-cols-3 xl:grid-cols-6">
        {metrics.map((m) => {
          const live = m.label === "Discovered" && overview.data ? overview.data : null;
          return (
            <div key={m.label} className="flex flex-col justify-between rounded-sm border border-stone bg-surface-container-lowest p-space-md">
              <div className="flex items-center justify-between text-outline">
                <span className="label-caps">{m.label}</span>
                <Icon name={m.icon} size={16} />
              </div>
              <div className="mt-space-sm">
                {live ? (
                  <>
                    <div className="font-mono text-data-metric text-on-surface">{live.jobs_active.toLocaleString()}</div>
                    <div className="mt-0.5 font-mono text-label-sm text-olive">+{live.jobs_new_24h} in 24h</div>
                  </>
                ) : (
                  <>
                    <div className="font-mono text-data-metric text-outline-variant" aria-label="No data yet">
                      —
                    </div>
                    <div className="mt-0.5 font-mono text-label-sm text-muted">No data yet</div>
                  </>
                )}
              </div>
            </div>
          );
        })}
      </section>

      <div className="grid grid-cols-1 items-start gap-space-lg xl:grid-cols-12">
        <div className="flex min-w-0 flex-col gap-space-lg xl:col-span-8">
          <Panel>
            <div className="p-space-md">
              <SectionHeader eyebrow="Stage gate 01" title="Priority opportunities awaiting review" />
            </div>
            <EmptyState
              icon="travel_explore"
              title="Ranking arrives with matching"
              action={
                <Link to="/discover" className="text-title-md text-on-surface underline underline-offset-4">
                  Browse {overview.data ? overview.data.jobs_active.toLocaleString() : ""} discovered jobs
                </Link>
              }
            >
              Opportunities are being discovered from approved job boards. Ranking against your verified profile, with eligibility and the
              evidence behind each score, arrives in Phase 4.
            </EmptyState>
          </Panel>

          <Panel>
            <div className="p-space-md">
              <SectionHeader eyebrow="Getting started" title="Set up your workspace" />
            </div>
            <SetupChecklist />
          </Panel>
        </div>

        <div className="flex flex-col gap-space-lg xl:col-span-4">
          <Panel>
            <div className="flex items-center justify-between p-space-md">
              <h2 className="font-serif text-headline-sm text-on-surface">Platform integrations</h2>
              <span className="label-caps text-outline">Live</span>
            </div>
            {meta.isPending && <p className="border-t border-stone p-space-md text-body-sm text-on-surface-variant">Checking integrations…</p>}
            {meta.isError && (
              <p className="border-t border-stone p-space-md text-body-sm text-terracotta" role="alert">
                {meta.error.message}
              </p>
            )}
            {meta.data && (
              <ul className="divide-y divide-stone border-t border-stone">
                {Object.entries(meta.data.integrations).map(([key, v]) => (
                  <li key={key} className="flex items-start justify-between gap-space-sm px-space-md py-space-sm">
                    <div className="min-w-0">
                      <p className="text-title-md text-on-surface">{integrationLabels[key] ?? key}</p>
                      {v.detail && <p className="text-body-sm text-muted">{v.detail}</p>}
                    </div>
                    <Tag tone={statusTone[v.status]}>{statusText[v.status]}</Tag>
                  </li>
                ))}
              </ul>
            )}
          </Panel>

          <section className="rounded-sm bg-primary-container p-space-md text-surface">
            <div className="flex items-center justify-between">
              <span className="label-caps text-secondary-fixed">Autonomous guardrails</span>
              <span className="label-caps rounded-sm border border-outline px-1.5 py-0.5 text-outline-variant">Human-approved</span>
            </div>
            <p className="mt-space-sm text-body-sm text-primary-fixed-dim">
              CareerPilot never sends an email or submits an application without your explicit approval. Autonomy settings,
              limits and the global kill switch arrive with the Automation Centre.
            </p>
          </section>
        </div>
      </div>
    </div>
  );
}
