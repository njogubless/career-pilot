import { Link } from "react-router";
import { Loading } from "../../components/ui/Loading";
import { EmptyState, Panel } from "../../components/ui/Panel";
import { Tag } from "../../components/ui/Tag";
import { relativeTime } from "../../lib/format";
import { kindLabel, useApplicationDocuments } from "../../lib/preparation";

/** Tailored application documents across all jobs (Phase 5). */
export function TailoredDocuments() {
  const docs = useApplicationDocuments();
  return (
    <Panel>
      <div className="p-space-md">
        <p className="label-caps text-outline">Application preparation</p>
        <h2 className="font-serif text-headline-sm text-on-surface">Tailored documents</h2>
      </div>
      {docs.isPending && <Loading />}
      {docs.isError && (
        <p role="alert" className="p-space-md text-terracotta">
          {docs.error.message}
        </p>
      )}
      {docs.data?.data.length === 0 && (
        <EmptyState icon="edit_document" title="No tailored documents yet">
          Open a job from <Link to="/discover" className="font-semibold underline">Discover</Link> and generate a CV, cover letter or application answers
          from your verified profile.
        </EmptyState>
      )}
      {docs.data && docs.data.data.length > 0 && (
        <ul className="divide-y divide-stone border-t border-stone">
          {docs.data.data.map((d) => (
            <li key={d.id} className="flex flex-wrap items-center justify-between gap-space-sm px-space-md py-space-sm">
              <div className="flex min-w-0 flex-col">
                <Link to={`/prepare/${d.id}`} className="font-semibold text-on-surface hover:underline">
                  {kindLabel[d.kind]}
                </Link>
                <span className="truncate text-body-sm text-on-surface-variant">
                  {d.job_title} · {d.company}
                </span>
              </div>
              <div className="flex items-center gap-space-sm">
                {d.approval_state === "CURRENT" ? (
                  <Tag tone="olive">Approved</Tag>
                ) : d.approval_state === "OUTDATED" ? (
                  <Tag tone="terracotta">Edited after approval</Tag>
                ) : d.current_version_id ? (
                  <Tag tone="stone">Draft</Tag>
                ) : (
                  <Tag tone="outline">Generating or failed</Tag>
                )}
                <span className="font-mono text-label-sm text-muted">{relativeTime(d.updated_at)}</span>
              </div>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
}
