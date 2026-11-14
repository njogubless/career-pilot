import { Panel } from "../../components/ui/Panel";
import { Tag, type TagTone } from "../../components/ui/Tag";
import { relativeTime, sourceLabel } from "../../lib/format";
import { useRuns, useSources, type SourceHealth } from "../../lib/jobs";

const tone: Record<SourceHealth, TagTone> = { HEALTHY: "citron", PENDING: "outline", DEGRADED: "terracotta", DISABLED: "stone" };

/** Live source health and recent runs (Stitch "Telemetry Ingestion Engine"). */
export function IngestionPanel() {
  const sources = useSources();
  const runs = useRuns();
  const healthy = sources.data?.filter((s) => s.health === "HEALTHY").length ?? 0;

  return (
    <Panel>
      <div className="flex items-center justify-between p-space-md">
        <div>
          <p className="label-caps text-outline">Telemetry</p>
          <h2 className="font-serif text-headline-sm text-on-surface">Ingestion engine</h2>
        </div>
        {sources.data && (
          <Tag tone={healthy === sources.data.length ? "citron" : "outline"}>
            {healthy}/{sources.data.length} healthy
          </Tag>
        )}
      </div>
      {sources.isError && <p className="border-t border-stone p-space-md text-body-sm text-terracotta">{sources.error.message}</p>}
      {sources.data && sources.data.length === 0 && (
        <p className="border-t border-stone p-space-md text-body-sm text-on-surface-variant">No job sources are configured on this server.</p>
      )}
      {sources.data && sources.data.length > 0 && (
        <ul className="max-h-80 divide-y divide-stone overflow-y-auto border-t border-stone">
          {sources.data.map((s) => (
            <li key={s.id} className="flex items-center justify-between gap-space-sm px-space-md py-space-xs">
              <div className="min-w-0">
                <p className="truncate text-title-md text-on-surface">{s.display_name}</p>
                <p className="font-mono text-label-sm text-muted">
                  {sourceLabel[s.kind]} · {s.active_jobs} active · {s.last_success_at ? relativeTime(s.last_success_at) : "never synced"}
                </p>
                {s.health === "DEGRADED" && s.last_error && <p className="truncate text-body-sm text-terracotta">{s.last_error}</p>}
              </div>
              <Tag tone={tone[s.health]}>{s.health}</Tag>
            </li>
          ))}
        </ul>
      )}
      <div className="border-t border-stone bg-primary-container p-space-md text-surface">
        <p className="label-caps text-secondary-fixed">Recent discovery runs</p>
        {runs.data && runs.data.length === 0 && <p className="mt-1 font-mono text-label-md text-primary-fixed-dim">No runs yet.</p>}
        <ul className="mt-space-xs space-y-1">
          {runs.data?.map((r) => (
            <li key={r.id} className="font-mono text-label-sm text-primary-fixed-dim">
              [{new Date(r.started_at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}] {r.source_name}:{" "}
              <span className={r.status === "FAILED" ? "text-on-tertiary-container" : r.status === "SUCCEEDED" ? "text-secondary-fixed" : ""}>
                {r.status.toLowerCase()}
              </span>
              {r.status === "SUCCEEDED" && ` +${r.created} new · ${r.updated} updated · ${r.expired} expired`}
              {r.status === "SKIPPED" && r.error ? ` (${r.error})` : ""}
            </li>
          ))}
        </ul>
      </div>
    </Panel>
  );
}
