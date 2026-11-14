import clsx from "clsx";
import { useMeta } from "../../lib/meta";

/**
 * Replaces the Stitch "Autonomous Active 99.4%" card with live platform status.
 * Shows real state only; no autonomous activity is implied until it exists.
 */
export function SystemStatusCard() {
  const meta = useMeta();
  const llm = meta.data?.integrations.llm?.status;

  let tone: "ok" | "warn" | "down" = "warn";
  let label = "Checking platform";
  let detail = "Contacting the CareerPilot API…";

  if (meta.isError) {
    tone = "down";
    label = "API unreachable";
    detail = "The core platform is not responding.";
  } else if (meta.data) {
    tone = llm === "available" ? "ok" : "warn";
    label = "Platform online";
    detail = llm === "available" ? "AI provider connected. Agents not yet enabled." : "AI provider not configured. Agents not yet enabled.";
  }

  return (
    <div className="flex flex-col gap-space-xs rounded-sm bg-surface/5 p-space-sm" role="status" aria-live="polite">
      <div className="flex items-center justify-between">
        <span
          className={clsx(
            "label-caps flex items-center gap-1.5",
            tone === "ok" && "text-secondary-fixed",
            tone === "warn" && "text-primary-fixed-dim",
            tone === "down" && "text-on-tertiary-container",
          )}
        >
          <span
            aria-hidden
            className={clsx(
              "h-1.5 w-1.5 rounded-full",
              tone === "ok" && "bg-secondary-fixed",
              tone === "warn" && "bg-outline-variant",
              tone === "down" && "bg-terracotta",
            )}
          />
          {label}
        </span>
        {meta.data && <span className="label-caps text-outline-variant">{meta.data.version}</span>}
      </div>
      <p className="text-body-sm leading-tight text-primary-fixed-dim">{detail}</p>
    </div>
  );
}
