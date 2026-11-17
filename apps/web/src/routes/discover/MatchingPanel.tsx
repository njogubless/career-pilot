import { Link } from "react-router";
import { Button } from "../../components/ui/Button";
import { Panel } from "../../components/ui/Panel";
import { Tag } from "../../components/ui/Tag";
import { relativeTime } from "../../lib/format";
import { useMatchState, useRecomputeMatches, useSkillGaps } from "../../lib/matching";

const statusText: Record<string, string> = {
  NOT_STARTED: "Not computed yet",
  QUEUED: "Queued",
  RUNNING: "Computing…",
  READY: "Up to date",
  FAILED: "Failed",
};

/** Matching status, manual recompute and aggregate skill gaps across top matches. */
export function MatchingPanel() {
  const state = useMatchState();
  const gaps = useSkillGaps();
  const recompute = useRecomputeMatches();
  const s = state.data;

  return (
    <Panel>
      <div className="flex items-center justify-between p-space-md">
        <div>
          <p className="label-caps text-outline">Matching engine</p>
          <h2 className="font-serif text-headline-sm text-on-surface">Your matches</h2>
        </div>
        {s && <Tag tone={s.status === "READY" ? "citron" : s.status === "FAILED" ? "terracotta" : "outline"}>{statusText[s.status]}</Tag>}
      </div>
      <div className="flex flex-col gap-space-xs border-t border-stone p-space-md text-body-sm text-on-surface-variant">
        {state.isError && <p className="text-terracotta">{state.error.message}</p>}
        {s?.status === "READY" && (
          <p>
            {s.scored} jobs scored ({s.algorithm_version}) {s.completed_at ? relativeTime(s.completed_at) : ""}. Candidates come from semantic similarity and skill overlap with your profile.
          </p>
        )}
        {s?.status === "NOT_STARTED" && (
          <p>
            Matches are computed from your profile. <Link to="/profile" className="font-semibold text-on-surface underline">Add skills and preferences</Link>, then recompute.
          </p>
        )}
        {s?.status === "FAILED" && s.error && <p className="text-terracotta">{s.error}</p>}
        {recompute.data && <p role="status">{recompute.data.message}</p>}
        {recompute.isError && <p className="text-terracotta">{recompute.error.message}</p>}
        <Button
          variant="secondary"
          icon="refresh"
          className="self-start"
          disabled={recompute.isPending || s?.status === "RUNNING" || s?.status === "QUEUED"}
          onClick={() => recompute.mutate()}
        >
          Recompute matches
        </Button>
      </div>
      {gaps.data && gaps.data.data.length > 0 && (
        <div className="border-t border-stone p-space-md">
          <p className="label-caps text-outline">Skill gaps across your top {gaps.data.matches_considered} matches</p>
          <ul className="mt-space-xs flex flex-col gap-1">
            {gaps.data.data.slice(0, 8).map((g) => (
              <li key={g.skill} className="flex items-center justify-between gap-space-sm text-body-sm">
                <span className="text-on-surface">{g.skill}</span>
                <span className="font-mono text-label-sm text-muted">
                  {g.jobs} {g.jobs === 1 ? "job" : "jobs"}
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </Panel>
  );
}
