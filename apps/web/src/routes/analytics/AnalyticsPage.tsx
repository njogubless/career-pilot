import { useState } from "react";
import { useSearchParams } from "react-router";
import { Button } from "../../components/ui/Button";
import { Loading } from "../../components/ui/Loading";
import { EmptyState, Panel } from "../../components/ui/Panel";
import { Tag } from "../../components/ui/Tag";
import { pct, useGenerateReport, useOverview, useReports, type Overview, type Report } from "../../lib/analytics";
import { relativeTime } from "../../lib/format";
import { FunnelBars, SkillHeatmap, StatTile, TableToggle, WeeklyColumns } from "./charts";

const periods = [
  [30, "30 days"],
  [90, "90 days"],
  [365, "12 months"],
  [0, "All time"],
] as const;

const fmtDays = (d: number | null) => (d == null ? "—" : d < 1 ? "< 1 day" : `${d.toFixed(1)} days`);

function Section({ eyebrow, title, action, children }: { eyebrow: string; title: string; action?: React.ReactNode; children: React.ReactNode }) {
  return (
    <Panel>
      <div className="flex items-end justify-between gap-space-sm border-b border-stone p-space-md">
        <div>
          <p className="label-caps text-outline">{eyebrow}</p>
          <h2 className="font-serif text-headline-sm text-on-surface">{title}</h2>
        </div>
        {action}
      </div>
      <div className="p-space-md">{children}</div>
    </Panel>
  );
}

function Funnel({ o }: { o: Overview }) {
  const [table, setTable] = useState(false);
  const rows = o.funnel.stages.map((s) => ({
    label: s.label,
    count: s.count,
    note: s.from_previous ? `${pct(s.from_previous)} of previous${s.from_previous.low_sample && s.from_previous.denominator > 0 ? " · small sample" : ""}` : undefined,
  }));
  return (
    <Section eyebrow="Application funnel" title="Where your applications got to" action={<TableToggle shown={table} onToggle={() => setTable(!table)} />}>
      <p className="mb-space-sm text-body-sm text-on-surface-variant">
        Applications started in this period, counted by the furthest stage each reached. Responses count only after a submission with evidence.
      </p>
      {table ? (
        <table className="w-full text-body-sm">
          <thead>
            <tr className="label-caps text-outline">
              <th className="py-1 text-left">Stage</th>
              <th className="py-1 text-right">Applications</th>
              <th className="py-1 text-right">From previous</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-stone">
            {o.funnel.stages.map((s) => (
              <tr key={s.key}>
                <td className="py-1">{s.label}</td>
                <td className="py-1 text-right font-mono">{s.count}</td>
                <td className="py-1 text-right font-mono">{s.from_previous ? `${pct(s.from_previous)} (${s.from_previous.numerator}/${s.from_previous.denominator})` : "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : (
        <FunnelBars rows={rows} />
      )}
      <p className="mt-space-sm font-mono text-label-sm text-muted">
        Also: {o.funnel.rejected} rejected · {o.funnel.withdrawn} withdrawn
      </p>
    </Section>
  );
}

function Weekly({ o }: { o: Overview }) {
  const [table, setTable] = useState(false);
  const weeks = o.weekly.map((w) => w.start);
  return (
    <Section eyebrow="Activity" title="Last 12 weeks" action={<TableToggle shown={table} onToggle={() => setTable(!table)} />}>
      {table ? (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[480px] text-body-sm">
            <thead>
              <tr className="label-caps text-outline">
                <th className="py-1 text-left">Week of</th>
                <th className="py-1 text-right">Strong matches</th>
                <th className="py-1 text-right">Applications</th>
                <th className="py-1 text-right">Submitted</th>
                <th className="py-1 text-right">Responses</th>
                <th className="py-1 text-right">Emails sent</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-stone">
              {o.weekly.map((w) => (
                <tr key={w.start}>
                  <td className="py-1">{new Date(w.start).toLocaleDateString()}</td>
                  <td className="py-1 text-right font-mono">{w.strong_matches}</td>
                  <td className="py-1 text-right font-mono">{w.applications}</td>
                  <td className="py-1 text-right font-mono">{w.submitted}</td>
                  <td className="py-1 text-right font-mono">{w.responses}</td>
                  <td className="py-1 text-right font-mono">{w.outreach_sent}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <WeeklyColumns
          weeks={weeks}
          series={[
            { label: "Applications started", values: o.weekly.map((w) => w.applications) },
            { label: "Submitted (with evidence)", values: o.weekly.map((w) => w.submitted) },
            { label: "Employer responses", values: o.weekly.map((w) => w.responses) },
          ]}
        />
      )}
    </Section>
  );
}

function Skills({ o }: { o: Overview }) {
  if (o.skill_trends.length === 0) {
    return (
      <Section eyebrow="Skill gaps" title="Missing requirements over time">
        <p className="text-body-sm text-muted">No skill gaps among your recent strong matches, or no strong matches yet.</p>
      </Section>
    );
  }
  return (
    <Section eyebrow="Skill gaps" title="Missing requirements over time">
      <p className="mb-space-sm text-body-sm text-on-surface-variant">
        Share of each week's strong matches (score 50+, not ineligible) that list the skill as a requirement your verified profile lacks.
      </p>
      <SkillHeatmap weeks={o.skill_weeks} rows={o.skill_trends} />
    </Section>
  );
}

function Sources({ o }: { o: Overview }) {
  return (
    <Section eyebrow="Source performance" title="Which boards work for you">
      {o.sources.length === 0 ? (
        <p className="text-body-sm text-muted">No matched jobs or applications in this period.</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[560px] text-body-sm">
            <thead>
              <tr className="label-caps text-outline">
                <th className="py-1 text-left">Source</th>
                <th className="py-1 text-right">Strong matches</th>
                <th className="py-1 text-right">Applications</th>
                <th className="py-1 text-right">Submitted</th>
                <th className="py-1 text-right">Responses</th>
                <th className="py-1 text-right">Response rate</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-stone">
              {o.sources.map((s) => (
                <tr key={s.source}>
                  <td className="py-1">{s.source}</td>
                  <td className="py-1 text-right font-mono">{s.jobs_matched}</td>
                  <td className="py-1 text-right font-mono">{s.applications}</td>
                  <td className="py-1 text-right font-mono">{s.submitted}</td>
                  <td className="py-1 text-right font-mono">{s.responses}</td>
                  <td className="py-1 text-right font-mono">
                    {pct(s.response_rate)}
                    {s.response_rate.denominator > 0 && s.response_rate.low_sample && <span className="ml-1 text-muted">(n={s.response_rate.denominator})</span>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <p className="mt-space-sm text-body-sm text-muted">Rates based on fewer than 5 submissions are shown with their sample size; they are not yet meaningful.</p>
    </Section>
  );
}

function ReportView({ r }: { r: Report }) {
  const d = r.data;
  return (
    <div className="flex flex-col gap-space-xs">
      <p className="font-mono text-label-sm text-muted">
        {r.trigger === "SCHEDULED"
          ? `Week of ${new Date(r.period_start).toLocaleDateString()} – ${new Date(r.period_end).toLocaleDateString()}`
          : `The 7 days before ${new Date(r.created_at).toLocaleString()}`}
      </p>
      <ul className="flex list-disc flex-col gap-0.5 pl-space-md text-body-md text-on-surface">
        {d.highlights.map((h) => (
          <li key={h}>{h}</li>
        ))}
      </ul>
      <p className="label-caps mt-space-xs text-outline">Recommendations</p>
      <ul className="flex list-disc flex-col gap-0.5 pl-space-md text-body-md text-on-surface-variant">
        {d.recommendations.map((h) => (
          <li key={h}>{h}</li>
        ))}
      </ul>
    </div>
  );
}

function Reports() {
  const reports = useReports();
  const gen = useGenerateReport();
  const [params, setParams] = useSearchParams();
  const selected = params.get("report");
  const list = reports.data?.data ?? [];
  const current = list.find((r) => r.id === selected) ?? list[0];
  return (
    <Section
      eyebrow="Career reports"
      title="Progress reports"
      action={
        <Button variant="secondary" icon="summarize" disabled={gen.isPending} onClick={() => gen.mutate(undefined, { onSuccess: (r) => setParams({ report: r.id }) })}>
          Report on the last 7 days
        </Button>
      }
    >
      {gen.isError && <p className="mb-space-sm text-body-sm text-terracotta">{gen.error.message}</p>}
      {list.length === 0 ? (
        <p className="text-body-sm text-muted">No reports yet. A weekly report is created each Monday when you have had activity; you can also make one now.</p>
      ) : (
        <div className="grid grid-cols-1 gap-space-md lg:grid-cols-3">
          <ul className="flex flex-col divide-y divide-stone">
            {list.slice(0, 10).map((r) => (
              <li key={r.id}>
                <button
                  type="button"
                  onClick={() => setParams({ report: r.id })}
                  className={"w-full py-1.5 text-left text-body-sm " + (r.id === current?.id ? "font-semibold text-on-surface" : "text-on-surface-variant")}
                >
                  {new Date(r.period_end).toLocaleDateString()} · {r.trigger === "SCHEDULED" ? "weekly" : "on demand"}
                  <span className="block font-mono text-label-sm text-muted">{relativeTime(r.created_at)}</span>
                </button>
              </li>
            ))}
          </ul>
          <div className="lg:col-span-2">{current && <ReportView r={current} />}</div>
        </div>
      )}
    </Section>
  );
}

/** Career Analytics: derived from persisted records only; no vanity counts of automated actions. */
export function AnalyticsPage() {
  const [period, setPeriod] = useState<number>(90);
  const q = useOverview(period);
  const o = q.data;
  const submitted = o?.funnel.stages.find((s) => s.key === "SUBMITTED");
  const responded = o?.funnel.stages.find((s) => s.key === "RESPONDED");
  const interviews = o?.funnel.stages.find((s) => s.key === "INTERVIEW");
  const offers = o?.funnel.stages.find((s) => s.key === "OFFER");
  return (
    <div className="flex flex-col gap-space-lg">
      <header className="flex max-w-3xl flex-col gap-space-xs">
        <p className="label-caps text-outline">Portfolio core</p>
        <h1 className="font-serif text-headline-lg-mobile text-on-surface md:text-display-lg">Career Analytics</h1>
        <p className="text-body-lg text-on-surface-variant">
          Measured from your own records: relevant opportunities, applications submitted with evidence, real responses. Not the number of things that
          were automated.
        </p>
      </header>
      <div role="group" aria-label="Period" className="flex flex-wrap gap-1">
        {periods.map(([v, label]) => (
          <button
            key={v}
            onClick={() => setPeriod(v)}
            aria-pressed={period === v}
            className={"rounded-sm border px-space-sm py-1 font-mono text-label-md " + (period === v ? "border-graphite bg-surface-container text-on-surface" : "border-stone text-on-surface-variant")}
          >
            {label}
          </button>
        ))}
      </div>
      {q.isPending && <Loading label="Computing analytics" />}
      {q.isError && <p role="alert" className="text-terracotta">{q.error.message}</p>}
      {o && (
        <>
          <Panel className="grid grid-cols-2 divide-stone md:grid-cols-3 md:divide-x xl:grid-cols-6">
            <StatTile label="Strong matches" value={String(o.funnel.jobs_matched)} note={`of ${o.funnel.jobs_discovered.toLocaleString()} jobs discovered`} />
            <StatTile label="Applications" value={String(o.funnel.stages[0]?.count ?? 0)} note="started in period" />
            <StatTile label="Submitted" value={String(submitted?.count ?? 0)} note="with evidence" />
            <StatTile label="Responses" value={String(responded?.count ?? 0)} note={responded?.from_previous ? `${pct(responded.from_previous)} of submitted` : undefined} />
            <StatTile label="Interviews" value={String(interviews?.count ?? 0)} note="assessment or interview" />
            <StatTile label="Offers" value={String(offers?.count ?? 0)} />
          </Panel>
          {o.funnel.stages[0]?.count === 0 && o.funnel.jobs_matched === 0 ? (
            <Panel>
              <EmptyState icon="analytics" title="Nothing to measure yet">
                Analytics appear as you shortlist, prepare and submit applications. Nothing here is estimated.
              </EmptyState>
            </Panel>
          ) : (
            <>
              <div className="grid grid-cols-1 items-start gap-space-lg xl:grid-cols-12">
                <div className="xl:col-span-7">
                  <Funnel o={o} />
                </div>
                <div className="flex flex-col gap-space-lg xl:col-span-5">
                  <Section eyebrow="Timing" title="Time between stages">
                    <table className="w-full text-body-sm">
                      <tbody className="divide-y divide-stone">
                        {o.durations.map((d) => (
                          <tr key={d.key}>
                            <td className="py-1.5">{d.label}</td>
                            <td className="py-1.5 text-right font-mono">{fmtDays(d.median_days)}</td>
                            <td className="py-1.5 text-right font-mono text-muted">n={d.samples}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                    <p className="mt-space-xs text-body-sm text-muted">Medians over all your applications.</p>
                  </Section>
                  <Section eyebrow="Outreach" title="Recruiter engagement">
                    <div className="grid grid-cols-2">
                      <StatTile label="Introductions sent" value={String(o.outreach.introductions_sent)} note={`${o.outreach.follow_ups_sent} follow-ups`} />
                      <StatTile
                        label="Reply rate"
                        value={pct(o.outreach.reply_rate)}
                        note={`${o.outreach.introductions_replied} replied · ${o.outreach.bounced} bounced${o.outreach.reply_rate.low_sample && o.outreach.reply_rate.denominator > 0 ? " · small sample" : ""}`}
                      />
                    </div>
                  </Section>
                </div>
              </div>
              <Weekly o={o} />
              <Skills o={o} />
              <Sources o={o} />
            </>
          )}
          <Reports />
          <p className="font-mono text-label-sm text-muted">
            Computed {relativeTime(o.computed_at)} <Tag tone="outline">from stored records</Tag>
          </p>
        </>
      )}
    </div>
  );
}
