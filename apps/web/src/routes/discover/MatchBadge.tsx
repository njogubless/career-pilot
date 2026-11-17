import clsx from "clsx";
import { eligibilityLabel, type MatchBrief } from "../../lib/matching";

/** Compact suitability badge for job cards. Never implies a score that was not computed. */
export function MatchBadge({ match }: { match: MatchBrief | null }) {
  if (!match) {
    return (
      <span className="label-caps rounded-xs border border-stone px-1.5 py-0.5 text-muted" title="No match computed for this job yet">
        Not scored
      </span>
    );
  }
  const ineligible = match.eligibility === "INELIGIBLE";
  const strong = !ineligible && match.score >= 70;
  return (
    <span
      className={clsx(
        "inline-flex items-baseline gap-1 rounded-xs px-1.5 py-0.5",
        ineligible ? "border border-terracotta text-terracotta" : strong ? "bg-citron text-graphite" : "border border-stone text-on-surface",
      )}
      title={`${eligibilityLabel[match.eligibility]} · ${match.confidence.toLowerCase()} confidence`}
    >
      <span className="font-mono text-[15px] font-semibold leading-none">{Math.round(match.score)}</span>
      <span className="label-caps">{ineligible ? "Not eligible" : match.eligibility === "UNKNOWN" ? "Match ?" : "Match"}</span>
    </span>
  );
}
