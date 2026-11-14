import { Link } from "react-router";
import { Icon } from "../../components/ui/Icon";
import { Tag } from "../../components/ui/Tag";
import { employmentLabel, formatSalary, relativeTime, remoteSummary, sourceLabel } from "../../lib/format";
import type { JobSummary } from "../../lib/jobs";

/** Job dossier card (Stitch "Discover Jobs"): company + source tags, editorial title, telemetry rows. */
export function JobCard({ job }: { job: JobSummary }) {
  const employment = employmentLabel(job.employment_type);
  return (
    <article className="rounded-sm border border-stone bg-surface-container-lowest p-space-md transition-colors hover:border-graphite/40">
      <div className="flex items-start justify-between gap-space-md">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-1">
            <Tag tone="stone">{job.company}</Tag>
            {job.department && <Tag tone="outline">{job.department}</Tag>}
            {employment && <Tag tone="outline">{employment}</Tag>}
          </div>
          <h3 className="mt-space-xs font-serif text-headline-sm text-on-surface">
            <Link to={`/jobs/${job.id}`} className="hover:underline hover:underline-offset-4">
              {job.title}
            </Link>
          </h3>
          <p className="mt-1 flex flex-wrap items-center gap-x-1 text-body-sm text-on-surface-variant">
            <Icon name="location_on" size={14} className="text-outline" />
            <span className="min-w-0 break-words">{job.location || "Location not stated"}</span>
            <span aria-hidden className="text-outline-variant">
              ·
            </span>
            <span>{remoteSummary(job)}</span>
          </p>
          <p className="mt-1 font-mono text-label-md text-on-surface">
            {job.salary ? (
              <>
                {formatSalary(job.salary)} <span className="text-olive">[Salary: disclosed]</span>
              </>
            ) : (
              <span className="text-muted">Salary not disclosed</span>
            )}
          </p>
        </div>
        <div className="flex shrink-0 flex-col items-end gap-1 text-right">
          <Tag tone="outline" className="text-muted">
            Match · Phase 4
          </Tag>
          <span className="font-mono text-label-sm text-muted">
            {sourceLabel[job.source.kind]} // {relativeTime(job.posted_at ?? job.first_seen_at)}
          </span>
        </div>
      </div>
      <div className="mt-space-sm flex items-center justify-between gap-space-sm border-t border-stone pt-space-sm">
        <span className="label-caps flex items-center gap-1.5 text-on-surface-variant">
          <span aria-hidden className="h-1.5 w-1.5 rounded-full bg-olive" />
          State: Discovered
        </span>
        <Link to={`/jobs/${job.id}`} className="inline-flex items-center gap-1 rounded-sm bg-graphite px-space-sm py-1.5 text-[13px] font-semibold text-ivory">
          Inspect dossier
        </Link>
      </div>
    </article>
  );
}
