import clsx from "clsx";
import { useState } from "react";
import { Link } from "react-router";
import { Button } from "../../components/ui/Button";
import { Icon } from "../../components/ui/Icon";
import { Panel } from "../../components/ui/Panel";
import { Tag, type TagTone } from "../../components/ui/Tag";
import { relativeTime } from "../../lib/format";
import {
  eligibilityLabel,
  useMatchFeedback,
  useRequestDeepAnalysis,
  type Check,
  type Dimension,
  type JobMatchResponse,
  type MatchResult,
  type SkillMatch,
} from "../../lib/matching";

const eligTone: Record<MatchResult["eligibility"], TagTone> = { ELIGIBLE: "citron", INELIGIBLE: "terracotta", UNKNOWN: "outline" };

function PanelHead({ eyebrow, title, aside }: { eyebrow: string; title: string; aside?: React.ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-space-md border-b border-stone p-space-md">
      <div>
        <p className="label-caps text-outline">{eyebrow}</p>
        <h2 className="font-serif text-headline-sm text-on-surface">{title}</h2>
      </div>
      {aside}
    </div>
  );
}

/** Explains why no score is shown, and what would produce one. */
function NotScored({ data }: { data: JobMatchResponse }) {
  const icon = data.status === "PROFILE_INCOMPLETE" ? "person_add" : data.status === "ANALYSIS_PENDING" ? "hourglass_top" : "cloud_off";
  return (
    <Panel>
      <PanelHead eyebrow="Matching engine" title="Suitability decomposition" />
      <div className="flex flex-col items-start gap-space-sm p-space-md">
        <Icon name={icon} size={24} className="text-olive" />
        <p className="text-body-md text-on-surface">{data.message}</p>
        {data.status === "PROFILE_INCOMPLETE" && (
          <Link to="/profile" className="inline-flex items-center gap-1 rounded-sm bg-graphite px-space-sm py-1.5 text-[13px] font-semibold text-ivory">
            Complete your profile
          </Link>
        )}
      </div>
    </Panel>
  );
}

function Meter({ d }: { d: Dimension }) {
  return (
    <div className="flex flex-col justify-between rounded-sm bg-surface-container p-space-sm">
      <span className="label-caps text-on-surface-variant">{d.label}</span>
      <div className="mt-space-xs flex items-end justify-between">
        {d.score === null ? (
          <span className="font-mono text-data-metric text-outline-variant" title="Not enough information; excluded from the score">
            —
          </span>
        ) : (
          <span className="font-mono text-data-metric text-on-surface">{Math.round(d.score)}</span>
        )}
        <span className="font-mono text-label-sm text-muted">{d.weight}% wt</span>
      </div>
      <div className="mt-2 h-1.5 w-full overflow-hidden rounded-full bg-surface-container-highest" aria-hidden>
        {d.score !== null && <div className="h-full rounded-full bg-secondary-fixed" style={{ width: `${d.score}%` }} />}
      </div>
      <p className="mt-1 text-body-sm text-muted">{d.score === null ? "Unknown: excluded from the score" : d.summary}</p>
    </div>
  );
}

export function SuitabilityPanel({ data }: { data: JobMatchResponse }) {
  if (!data.match) return <NotScored data={data} />;
  const m = data.match;
  return (
    <Panel>
      <div className="flex flex-col gap-space-md p-space-md">
        <div className="flex items-start justify-between gap-space-md">
          <div>
            <p className="label-caps flex items-center gap-1.5 text-outline">
              <span aria-hidden className="h-2 w-2 rounded-full bg-secondary-fixed" /> Matching engine · {m.algorithm_version}
            </p>
            <h2 className="font-serif text-headline-md text-on-surface">Suitability decomposition</h2>
            <div className="mt-space-xs flex flex-wrap gap-1">
              <Tag tone={eligTone[m.eligibility]}>{eligibilityLabel[m.eligibility]}</Tag>
              <Tag tone="outline">{m.confidence.toLowerCase()} confidence</Tag>
            </div>
          </div>
          <div className="flex shrink-0 flex-col items-end rounded-sm bg-secondary-container px-3 py-2 text-on-secondary-fixed" aria-label={`Suitability score ${Math.round(m.score)} out of 100`}>
            <span className="label-caps">Suitability</span>
            <span className="flex items-baseline gap-1">
              <span className="font-mono text-[34px] font-bold leading-tight">{Math.round(m.score)}</span>
              <span className="font-mono text-label-md">/100</span>
            </span>
            <span className="label-caps text-on-secondary-container">{Math.round(m.known_weight)}% of weight known</span>
          </div>
        </div>
        <div className="flex flex-col gap-space-xs rounded-sm bg-surface-container-low p-space-md">
          <p className="label-caps flex items-center gap-2 text-secondary">
            <Icon name="psychology" size={18} /> Score explanation · computed {data.computed_at ? relativeTime(data.computed_at) : ""}
          </p>
          {m.explanation.map((line) => (
            <p key={line} className="text-body-md text-on-surface">
              {line}
            </p>
          ))}
        </div>
        <div className="grid grid-cols-1 gap-space-sm sm:grid-cols-2 xl:grid-cols-3">
          {m.dimensions.map((d) => (
            <Meter key={d.key} d={d} />
          ))}
        </div>
        <p className="text-body-sm text-muted">{data.disclaimer}</p>
      </div>
    </Panel>
  );
}

const importanceLabel: Record<SkillMatch["importance"], string> = { REQUIRED: "Core", MENTIONED: "Role", PREFERRED: "Nice to have" };

function SkillStatus({ s }: { s: SkillMatch }) {
  switch (s.status) {
    case "MATCHED":
      return (
        <Tag tone="citron">
          <Icon name="check" size={14} /> Verified match
        </Tag>
      );
    case "PARTIAL":
      return <Tag tone="stone">Partial evidence</Tag>;
    case "MISSING":
      return <Tag tone={s.importance === "PREFERRED" ? "outline" : "terracotta"}>No evidence</Tag>;
    default:
      return <Tag tone="outline">Unknown</Tag>;
  }
}

export function SkillMatrix({ match }: { match: MatchResult }) {
  if (match.skills.length === 0) {
    return (
      <Panel>
        <PanelHead eyebrow="Taxonomy mapping" title="Skill matrix" />
        <p className="p-space-md text-body-md text-on-surface-variant">No skill requirements could be identified in this posting, so skills are excluded from the score.</p>
      </Panel>
    );
  }
  const assessed = match.skills.filter((s) => s.status === "MATCHED" || s.status === "PARTIAL").length;
  return (
    <Panel>
      <PanelHead
        eyebrow="Taxonomy mapping"
        title="Skill matrix"
        aside={
          <span className="font-mono text-label-sm text-on-surface-variant">
            {assessed}/{match.skills.length} evidenced
          </span>
        }
      />
      <div className="overflow-x-auto">
        <table className="w-full border-collapse text-left">
          <thead>
            <tr className="label-caps bg-surface-container-low text-on-surface-variant">
              <th scope="col" className="px-3 py-2.5">
                Requirement
              </th>
              <th scope="col" className="px-3 py-2.5">
                Weight
              </th>
              <th scope="col" className="px-3 py-2.5">
                Your evidence
              </th>
              <th scope="col" className="px-3 py-2.5 text-right">
                Alignment
              </th>
            </tr>
          </thead>
          <tbody>
            {match.skills.map((s, i) => (
              <tr key={s.name} className={clsx("align-top", i % 2 ? "bg-surface-container-low/50" : "bg-surface-container-lowest")}>
                <td className="px-3 py-3">
                  <div className="text-title-md text-on-surface">{s.name}</div>
                  <q className="block max-w-sm text-body-sm text-muted">{s.job_evidence}</q>
                </td>
                <td className="px-3 py-3 font-mono text-label-md text-on-surface">{importanceLabel[s.importance]}</td>
                <td className="px-3 py-3 text-body-sm text-on-surface-variant">{s.profile_evidence ?? <span className="text-muted">Nothing on your profile</span>}</td>
                <td className="px-3 py-3 text-right">
                  <SkillStatus s={s} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Panel>
  );
}

const checkIcon: Record<Check["status"], { name: string; cls: string; label: string }> = {
  PASS: { name: "check_circle", cls: "text-olive", label: "Passed" },
  FAIL: { name: "cancel", cls: "text-terracotta", label: "Blocker" },
  UNKNOWN: { name: "help", cls: "text-outline", label: "Unknown" },
};

export function EligibilityPanel({ match }: { match: MatchResult }) {
  const negatives = match.signals.filter((s) => s.kind === "NEGATIVE");
  const positives = match.signals.filter((s) => s.kind === "POSITIVE");
  return (
    <Panel>
      <PanelHead eyebrow="Hard filters · never guessed" title="Eligibility & flags" />
      <ul className="divide-y divide-stone">
        {match.checks.map((c) => {
          const ic = checkIcon[c.status];
          return (
            <li key={c.key} className="flex gap-space-sm px-space-md py-space-sm">
              <Icon name={ic.name} size={20} className={ic.cls} />
              <div className="min-w-0">
                <p className="text-title-md text-on-surface">
                  {c.label} <span className="sr-only">: {ic.label}</span>
                </p>
                <p className="text-body-sm text-on-surface-variant">{c.detail}</p>
                {c.evidence && <q className="mt-0.5 block text-body-sm text-muted">{c.evidence}</q>}
              </div>
            </li>
          );
        })}
      </ul>
      {(positives.length > 0 || negatives.length > 0) && (
        <div className="grid grid-cols-1 gap-space-md border-t border-stone p-space-md md:grid-cols-2">
          <div>
            <p className="label-caps text-olive">Positive signals</p>
            <ul className="mt-1 list-disc space-y-1 pl-4 text-body-sm text-on-surface">
              {positives.map((s) => (
                <li key={s.text}>{s.text}</li>
              ))}
              {positives.length === 0 && <li className="list-none text-muted">None identified.</li>}
            </ul>
          </div>
          <div>
            <p className="label-caps text-terracotta">Negative signals</p>
            <ul className="mt-1 list-disc space-y-1 pl-4 text-body-sm text-on-surface">
              {negatives.map((s) => (
                <li key={s.text}>{s.text}</li>
              ))}
              {negatives.length === 0 && <li className="list-none text-muted">None identified.</li>}
            </ul>
          </div>
        </div>
      )}
      {match.unknowns.length > 0 && (
        <details className="border-t border-stone px-space-md py-space-sm">
          <summary className="cursor-pointer text-body-sm font-semibold text-on-surface">Unknown information ({match.unknowns.length})</summary>
          <ul className="mt-1 list-disc space-y-1 pl-4 text-body-sm text-on-surface-variant">
            {match.unknowns.map((u) => (
              <li key={u}>{u}</li>
            ))}
          </ul>
        </details>
      )}
    </Panel>
  );
}

const deepStatusText: Record<string, string> = {
  QUEUED: "Deep analysis queued…",
  RUNNING: "Deep analysis running…",
  FAILED: "Deep analysis failed.",
};

export function AnalysisPanel({ jobId, data }: { jobId: string; data: JobMatchResponse }) {
  const deep = useRequestDeepAnalysis(jobId);
  const a = data.analysis;
  const busy = a.deep_status === "QUEUED" || a.deep_status === "RUNNING";
  return (
    <Panel>
      <PanelHead eyebrow="Provenance" title="Requirement analysis" />
      <dl className="grid grid-cols-[auto_1fr] gap-x-space-md gap-y-1 p-space-md text-body-sm">
        <dt className="text-muted">Method</dt>
        <dd className="text-on-surface">
          {a.method === "LLM" ? `AI extraction${a.model ? ` (${a.model})` : ""}` : a.method === "DETERMINISTIC" ? "Deterministic (taxonomy + rules)" : "Not analysed yet"}
        </dd>
        {a.analyzer_version && (
          <>
            <dt className="text-muted">Analyser</dt>
            <dd className="font-mono text-on-surface">{a.analyzer_version}</dd>
          </>
        )}
        {a.analyzed_at && (
          <>
            <dt className="text-muted">Analysed</dt>
            <dd className="text-on-surface">{relativeTime(a.analyzed_at)}</dd>
          </>
        )}
        <dt className="text-muted">Similarity</dt>
        <dd className="text-on-surface">{a.embedded ? "Embedded (semantic retrieval on)" : "Not embedded"}</dd>
      </dl>
      <div className="flex flex-col gap-space-xs border-t border-stone p-space-md">
        {a.method !== "LLM" && (
          <p className="text-body-sm text-on-surface-variant">
            Deterministic analysis cannot fully separate required from optional skills. Deep analysis uses the AI provider to read the posting; every extracted requirement must quote the posting or it is discarded.
          </p>
        )}
        {busy && (
          <p role="status" className="text-body-sm text-on-surface">
            {deepStatusText[a.deep_status]}
          </p>
        )}
        {a.deep_status === "FAILED" && <p className="text-body-sm text-terracotta">{a.deep_error ?? deepStatusText.FAILED}</p>}
        {deep.isError && <p className="text-body-sm text-terracotta">{deep.error.message}</p>}
        {deep.data && !deep.data.queued && <p className="text-body-sm text-on-surface-variant">{deep.data.message}</p>}
        {a.method !== "LLM" &&
          (data.deep_analysis_available ? (
            <Button variant="secondary" icon="auto_fix_high" disabled={busy || deep.isPending} onClick={() => deep.mutate()}>
              Run deep analysis
            </Button>
          ) : (
            <p className="text-body-sm text-muted">Deep analysis is unavailable: no AI provider is configured on this server.</p>
          ))}
      </div>
    </Panel>
  );
}

const reasons: { value: string; label: string }[] = [
  { value: "GOOD_FIT", label: "Good fit" },
  { value: "SKILLS_MISMATCH", label: "Skills don't match" },
  { value: "WRONG_SENIORITY", label: "Wrong seniority" },
  { value: "LOCATION", label: "Location doesn't work" },
  { value: "COMPENSATION", label: "Compensation" },
  { value: "NOT_INTERESTED", label: "Not interested" },
  { value: "SCORE_TOO_HIGH", label: "Score seems too high" },
  { value: "SCORE_TOO_LOW", label: "Score seems too low" },
  { value: "OTHER", label: "Other" },
];

export function FeedbackPanel({ jobId, data }: { jobId: string; data: JobMatchResponse }) {
  const { save, clear } = useMatchFeedback(jobId);
  const fb = data.feedback;
  const [reason, setReason] = useState<string>(fb?.reason ?? "");
  const send = (rating: "UP" | "DOWN", dismissed = fb?.dismissed ?? false) => save.mutate({ rating, reason: reason || null, dismissed });
  return (
    <Panel>
      <PanelHead eyebrow="Calibration" title="Your feedback" />
      <div className="flex flex-col gap-space-sm p-space-md">
        <div className="flex flex-wrap gap-space-xs">
          <Button variant={fb?.rating === "UP" ? "primary" : "secondary"} icon="thumb_up" aria-pressed={fb?.rating === "UP"} onClick={() => send("UP")} disabled={save.isPending}>
            Relevant
          </Button>
          <Button variant={fb?.rating === "DOWN" ? "primary" : "secondary"} icon="thumb_down" aria-pressed={fb?.rating === "DOWN"} onClick={() => send("DOWN")} disabled={save.isPending}>
            Not relevant
          </Button>
        </div>
        <label className="flex flex-col gap-1 text-body-sm text-on-surface-variant">
          Reason (optional)
          <select className="rounded-sm border border-stone bg-surface-container-lowest px-2 py-1.5 text-body-sm text-on-surface" value={reason} onChange={(e) => setReason(e.target.value)}>
            <option value="">—</option>
            {reasons.map((r) => (
              <option key={r.value} value={r.value}>
                {r.label}
              </option>
            ))}
          </select>
        </label>
        <div className="flex flex-wrap gap-space-xs">
          {fb?.dismissed ? (
            <Button variant="ghost" icon="undo" onClick={() => send(fb.rating, false)} disabled={save.isPending}>
              Restore to my matches
            </Button>
          ) : (
            <Button variant="destructive" icon="block" onClick={() => send("DOWN", true)} disabled={save.isPending}>
              Dismiss
            </Button>
          )}
          {fb && (
            <Button variant="ghost" onClick={() => clear.mutate()} disabled={clear.isPending}>
              Clear feedback
            </Button>
          )}
        </div>
        {save.isError && <p className="text-body-sm text-terracotta">{save.error.message}</p>}
        {fb && <p className="text-body-sm text-muted">Saved {relativeTime(fb.updated_at)}{fb.dismissed ? " · hidden from your matches" : ""}.</p>}
        <p className="text-body-sm text-muted">Feedback is stored to evaluate and improve matching. It does not change this score.</p>
      </div>
    </Panel>
  );
}

export function GapsPanel({ match }: { match: MatchResult }) {
  if (match.missing_skills.length === 0 && match.missing_preferred.length === 0) return null;
  return (
    <Panel>
      <PanelHead eyebrow="Skill gap analysis" title="What this role asks for that your profile does not show" />
      <div className="flex flex-col gap-space-sm p-space-md">
        {match.missing_skills.length > 0 && (
          <div>
            <p className="label-caps text-terracotta">Core</p>
            <div className="mt-1 flex flex-wrap gap-1">
              {match.missing_skills.map((s) => (
                <Tag key={s} tone="terracotta">
                  {s}
                </Tag>
              ))}
            </div>
          </div>
        )}
        {match.missing_preferred.length > 0 && (
          <div>
            <p className="label-caps text-outline">Nice to have</p>
            <div className="mt-1 flex flex-wrap gap-1">
              {match.missing_preferred.map((s) => (
                <Tag key={s} tone="outline">
                  {s}
                </Tag>
              ))}
            </div>
          </div>
        )}
        <p className="text-body-sm text-muted">If you have these skills, add them to your profile; they will then count as evidence.</p>
      </div>
    </Panel>
  );
}
