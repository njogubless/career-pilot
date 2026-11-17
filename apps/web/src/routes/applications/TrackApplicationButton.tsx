import { Link, useNavigate } from "react-router";
import { Button } from "../../components/ui/Button";
import { statusLabel, useCreateApplication, useJobApplication } from "../../lib/applications";

/** Job page entry into the pipeline: track the job, or open its application. */
export function TrackApplicationButton({ jobId }: { jobId: string }) {
  const existing = useJobApplication(jobId);
  const create = useCreateApplication();
  const navigate = useNavigate();
  if (existing.isPending) return null;
  const a = existing.data?.application;
  if (a) {
    return (
      <Link
        to={`/applications/${a.id}`}
        className="inline-flex items-center gap-2 rounded-sm border border-stone px-space-md py-2 text-[14px] font-semibold text-on-surface hover:border-graphite"
      >
        Application · {statusLabel[a.status]}
      </Link>
    );
  }
  return (
    <div className="flex flex-col gap-1">
      <Button variant="secondary" icon="view_kanban" disabled={create.isPending} onClick={() => create.mutate(jobId, { onSuccess: (d) => navigate(`/applications/${d.id}`) })}>
        Track application
      </Button>
      {create.isError && <p className="text-body-sm text-terracotta">{create.error.message}</p>}
    </div>
  );
}
