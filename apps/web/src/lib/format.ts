import type { JobSummary, Salary, SourceKind } from "./jobs";

export const sourceLabel: Record<SourceKind, string> = {
  GREENHOUSE: "Greenhouse",
  LEVER: "Lever",
  ASHBY: "Ashby",
  REMOTEOK: "Remote OK",
};

/** "3h ago", "2d ago" … from an ISO timestamp. */
export function relativeTime(iso: string | null, now = Date.now()): string {
  if (!iso) return "date unknown";
  const s = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000));
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  if (s < 86400 * 30) return `${Math.floor(s / 86400)}d ago`;
  return new Date(iso).toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" });
}

const periodLabel = { YEAR: "yr", MONTH: "mo", HOUR: "hr" } as const;

/** Formats a disclosed salary exactly as published (no conversion). */
export function formatSalary(s: Salary): string {
  const fmt = (v: number) =>
    new Intl.NumberFormat(undefined, { style: "currency", currency: s.currency, notation: v >= 10000 ? "compact" : "standard", maximumFractionDigits: v >= 10000 ? 1 : 0 }).format(v);
  const range = s.min != null && s.max != null ? `${fmt(s.min)} – ${fmt(s.max)}` : s.min != null ? `from ${fmt(s.min)}` : `up to ${fmt(s.max!)}`;
  return `${range} / ${periodLabel[s.period]}`;
}

/** Plain-language remote/eligibility summary; unknowns are stated as unknown. */
export function remoteSummary(j: Pick<JobSummary, "remote_status" | "remote_scope" | "remote_countries" | "remote_region">): string {
  switch (j.remote_status) {
    case "REMOTE":
      switch (j.remote_scope) {
        case "WORLDWIDE":
          return "Remote · worldwide";
        case "COUNTRIES":
          return `Remote · ${j.remote_countries.join(", ")} only`;
        case "REGION":
          return `Remote · ${j.remote_region}`;
        default:
          return "Remote · applicant location not stated";
      }
    case "HYBRID":
      return "Hybrid";
    case "ONSITE":
      return "On-site";
    default:
      return "Work arrangement not stated";
  }
}

export function employmentLabel(t: string): string | null {
  return t === "UNKNOWN" ? null : t.charAt(0) + t.slice(1).toLowerCase().replace("_", "-");
}
