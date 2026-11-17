import { Link, useParams } from "react-router";
import { Icon } from "../../components/ui/Icon";
import { Loading } from "../../components/ui/Loading";
import { Panel } from "../../components/ui/Panel";
import { Tag } from "../../components/ui/Tag";
import { ApiError } from "../../lib/api";
import { employmentLabel, formatSalary, relativeTime, remoteSummary, sourceLabel } from "../../lib/format";
import { useJob, type JobDetail } from "../../lib/jobs";
import { useJobMatch } from "../../lib/matching";
import { MatchBadge } from "./MatchBadge";
import { PreparationPanel } from "./PreparationPanel";
import { AnalysisPanel, EligibilityPanel, FeedbackPanel, GapsPanel, SkillMatrix, SuitabilityPanel } from "./JobIntelligence";

function Attribution({ job }: { job: JobDetail }) {
  // Remote OK's API terms require a followed link back and naming them as the
  // source, so this link deliberately omits rel="nofollow".
  const rel = job.source.attribution_required ? "noopener" : "noopener noreferrer";
  return (
    <p className="text-body-sm text-on-surface-variant">
      Source:{" "}
      <a href={job.canonical_url} target="_blank" rel={rel} className="font-semibold text-on-surface underline underline-offset-4">
        {job.source.attribution_required ? `${sourceLabel[job.source.kind]} (original listing)` : `${job.source.name} on ${sourceLabel[job.source.kind]}`}
      </a>
      {" · "}first seen {relativeTime(job.first_seen_at)}, last confirmed {relativeTime(job.last_seen_at)}
    </p>
  );
}

export function JobDetailPage() {
  const { id = "" } = useParams();
  const job = useJob(id);
  const match = useJobMatch(id);

  if (job.isPending) return <Loading label="Loading dossier" />;
  if (job.isError) {
    return (
      <Panel className="p-space-lg">
        <p role="alert" className="text-body-md text-terracotta">
          {job.error instanceof ApiError && job.error.status === 404 ? "This job does not exist." : job.error.message}
        </p>
        <Link to="/discover" className="mt-space-sm inline-block underline">
          Back to discovery
        </Link>
      </Panel>
    );
  }
  const j = job.data;
  const employment = employmentLabel(j.employment_type);

  return (
    <div className="flex flex-col gap-space-lg">
      <div className="flex flex-wrap items-center justify-between gap-space-sm">
        <nav aria-label="Breadcrumb" className="font-mono text-label-md text-muted">
          <Link to="/discover" className="hover:underline">
            Discover jobs
          </Link>{" "}
          / {j.company} / <span className="text-on-surface">{j.title}</span>
        </nav>
        {match.data?.match && (
          <MatchBadge match={{ score: match.data.match.score, eligibility: match.data.match.eligibility, confidence: match.data.match.confidence }} />
        )}
      </div>

      {j.expired_at && (
        <div role="status" className="rounded-sm border border-terracotta bg-tertiary-fixed/40 px-space-md py-space-sm text-body-md text-on-surface">
          This posting was no longer listed on {relativeTime(j.expired_at)}. It is kept for your records but is likely closed.
        </div>
      )}

      <header className="grid grid-cols-1 gap-space-lg lg:grid-cols-12">
        <div className="flex flex-col gap-space-sm lg:col-span-8">
          <div className="flex flex-wrap items-center gap-1">
            <Tag tone="stone">{j.company}</Tag>
            {j.department && <Tag tone="outline">{j.department}</Tag>}
            {employment && <Tag tone="outline">{employment}</Tag>}
            <Tag tone="outline">{remoteSummary(j)}</Tag>
          </div>
          <h1 className="font-serif text-headline-lg-mobile text-on-surface md:text-display-lg">{j.title}</h1>
          <p className="flex items-center gap-1 text-body-md text-on-surface-variant">
            <Icon name="location_on" size={16} className="text-outline" /> {j.location || "Location not stated"}
          </p>
          <Attribution job={j} />
          <div className="mt-space-xs flex flex-wrap gap-space-sm">
            <a
              href={j.apply_url}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-2 rounded-sm bg-graphite px-space-md py-2 text-[14px] font-semibold text-ivory hover:outline hover:outline-1 hover:outline-offset-2 hover:outline-citron"
            >
              <Icon name="open_in_new" size={16} className="text-secondary-fixed" /> Apply on the official site
            </a>
          </div>
          <p className="text-body-sm text-muted">
            Prepare tailored documents below. Tracked, approved submissions arrive in Phase 6; until then, applying opens the employer's official page.
          </p>
        </div>
        <Panel className="flex flex-col gap-space-sm p-space-md lg:col-span-4">
          <span className="label-caps text-outline">Base salary</span>
          {j.salary ? (
            <>
              <span className="font-mono text-data-metric text-on-surface">{formatSalary(j.salary)}</span>
              <span className="label-caps text-olive">Disclosed by the employer · {j.salary.currency}</span>
            </>
          ) : (
            <>
              <span className="font-mono text-data-metric text-outline-variant">—</span>
              <span className="text-body-sm text-muted">{j.salary_text ? `Published as: “${j.salary_text}” (not parsed)` : "Not disclosed. CareerPilot does not estimate salaries."}</span>
            </>
          )}
        </Panel>
      </header>

      <div className="grid grid-cols-1 items-start gap-space-lg lg:grid-cols-12">
        <div className="flex min-w-0 flex-col gap-space-lg lg:col-span-7">
          {match.isPending && <Loading label="Computing suitability" />}
          {match.isError && (
            <Panel className="p-space-md">
              <p role="alert" className="text-body-md text-terracotta">
                Suitability could not be loaded: {match.error.message}
              </p>
            </Panel>
          )}
          {match.data && <SuitabilityPanel data={match.data} />}
          {match.data?.match && <SkillMatrix match={match.data.match} />}
          {match.data?.match && <EligibilityPanel match={match.data.match} />}
          <Panel>
            <div className="border-b border-stone p-space-md">
              <p className="label-caps text-outline">Source ingestion</p>
              <h2 className="font-serif text-headline-sm text-on-surface">Job description</h2>
            </div>
            {j.description_html ? (
              // Sanitised server-side (bluemonday UGC policy, no scripts/styles/handlers); CSP blocks inline scripts.
              <div className="job-prose p-space-md" dangerouslySetInnerHTML={{ __html: j.description_html }} />
            ) : (
              <p className="p-space-md text-body-md text-muted">The source did not provide a description.</p>
            )}
          </Panel>
        </div>
        <aside className="flex min-w-0 flex-col gap-space-lg lg:col-span-5">
          <PreparationPanel jobId={id} />
          {match.data?.match && <GapsPanel match={match.data.match} />}
          {match.data && <AnalysisPanel jobId={id} data={match.data} />}
          {match.data && match.data.status === "READY" && <FeedbackPanel key={match.data.feedback?.updated_at ?? "none"} jobId={id} data={match.data} />}
          {j.tags.length > 0 && (
            <Panel className="p-space-md">
              <p className="label-caps text-outline">Source tags</p>
              <div className="mt-space-xs flex flex-wrap gap-1">
                {j.tags.map((t) => (
                  <Tag key={t} tone="outline">
                    {t}
                  </Tag>
                ))}
              </div>
            </Panel>
          )}
        </aside>
      </div>
    </div>
  );
}
