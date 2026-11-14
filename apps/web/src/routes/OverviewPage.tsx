import { Link } from "react-router";
import { EmptyState, Panel, SectionHeader } from "../components/ui/Panel";
import { Tag, type TagTone } from "../components/ui/Tag";
import { Icon } from "../components/ui/Icon";
import { useMeta, type IntegrationStatus } from "../lib/meta";

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

const setupSteps = [
  { to: "/profile", label: "Build your professional profile", phase: 2 },
  { to: "/documents", label: "Upload your CV for extraction", phase: 2 },
  { to: "/profile", label: "Set career preferences and constraints", phase: 2 },
  { to: "/discover", label: "Run your first discovery cycle", phase: 3 },
];

export function OverviewPage() {
  const meta = useMeta();

  return (
    <div className="flex flex-col gap-space-lg">
      <Panel className="flex flex-col gap-space-xs p-space-lg">
        <div className="flex flex-wrap items-center gap-space-xs">
          <Tag tone="graphite">Portfolio overview</Tag>
          <span className="label-caps text-outline">Foundation release</span>
        </div>
        <h1 className="mt-1 font-serif text-headline-lg-mobile tracking-tight text-on-surface md:text-headline-lg">Career execution, under your control.</h1>
        <p className="max-w-2xl text-body-lg text-on-surface-variant">
          CareerPilot discovers relevant roles, explains how well they fit, prepares truthful applications and asks for your
          approval before anything leaves the platform. Your workspace fills in as you complete each step below.
        </p>
      </Panel>

      <section aria-label="Key metrics" className="grid grid-cols-2 gap-space-sm md:grid-cols-3 xl:grid-cols-6">
        {metrics.map((m) => (
          <div key={m.label} className="flex flex-col justify-between rounded-sm border border-stone bg-surface-container-lowest p-space-md">
            <div className="flex items-center justify-between text-outline">
              <span className="label-caps">{m.label}</span>
              <Icon name={m.icon} size={16} />
            </div>
            <div className="mt-space-sm">
              <div className="font-mono text-data-metric text-outline-variant" aria-label="No data yet">
                —
              </div>
              <div className="mt-0.5 font-mono text-label-sm text-muted">No data yet</div>
            </div>
          </div>
        ))}
      </section>

      <div className="grid grid-cols-1 items-start gap-space-lg xl:grid-cols-12">
        <div className="flex min-w-0 flex-col gap-space-lg xl:col-span-8">
          <Panel>
            <div className="p-space-md">
              <SectionHeader eyebrow="Stage gate 01" title="Priority opportunities awaiting review" />
            </div>
            <EmptyState icon="travel_explore" title="No opportunities yet">
              Ranked opportunities appear here once your profile is complete and a discovery cycle has searched approved job
              boards. Each one will show its match score, eligibility and the evidence behind it.
            </EmptyState>
          </Panel>

          <Panel>
            <div className="p-space-md">
              <SectionHeader eyebrow="Getting started" title="Set up your workspace" />
            </div>
            <ol className="divide-y divide-stone border-t border-stone">
              {setupSteps.map((s, i) => (
                <li key={s.label} className="flex items-center justify-between gap-space-md px-space-md py-space-sm">
                  <span className="flex items-center gap-space-sm">
                    <span className="font-mono text-label-md text-outline">{String(i + 1).padStart(2, "0")}</span>
                    <Link to={s.to} className="text-title-md text-on-surface underline-offset-4 hover:underline">
                      {s.label}
                    </Link>
                  </span>
                  <Tag tone="outline">Phase {s.phase}</Tag>
                </li>
              ))}
            </ol>
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
