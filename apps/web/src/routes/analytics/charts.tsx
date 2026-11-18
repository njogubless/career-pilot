import { useState, type ReactNode } from "react";

/*
 * Charts follow the dataviz method: one validated hue for magnitude
 * (#556b2f, 5.9:1 on white), a monotonic one-hue ramp for the heatmap,
 * thin marks (<= 24px, 4px rounded data end, square at the baseline, 2px
 * surface gaps), text in text tokens, hover + focus tooltips, and a table
 * view for every chart. No categorical palette is needed: multi-measure
 * views are small multiples sharing one axis.
 */
const MARK = "#556b2f";
const RAMP = ["#f0f2e6", "#d2dab6", "#a7b97c", "#788f48", "#4b5f26"];
const GRID = "#e5e2da";

export function StatTile({ label, value, note }: { label: string; value: string; note?: string }) {
  return (
    <div className="flex flex-col gap-0.5 p-space-md">
      <span className="text-body-sm text-on-surface-variant">{label}</span>
      <span className="font-sans text-[28px] font-semibold leading-tight text-on-surface">{value}</span>
      {note && <span className="text-body-sm text-muted">{note}</span>}
    </div>
  );
}

/** A tooltip anchored inside a relatively positioned chart frame. */
function Tip({ x, y, children }: { x: number | string; y: number; children: ReactNode }) {
  return (
    <div
      role="tooltip"
      className="pointer-events-none absolute z-10 w-max max-w-[220px] -translate-x-1/2 -translate-y-full rounded-sm border border-graphite/20 bg-surface-container-lowest px-2 py-1 text-body-sm text-on-surface shadow-overlay"
      style={{ left: x, top: y - 6 }}
    >
      {children}
    </div>
  );
}

export function TableToggle({ shown, onToggle }: { shown: boolean; onToggle: () => void }) {
  return (
    <button type="button" onClick={onToggle} className="font-mono text-label-sm text-on-surface-variant underline">
      {shown ? "Show chart" : "Show as table"}
    </button>
  );
}

export type FunnelRow = { label: string; count: number; note?: string };

/** Ranked horizontal bars, one hue; value at the tip; conversion in the label column. */
export function FunnelBars({ rows }: { rows: FunnelRow[] }) {
  const [hover, setHover] = useState<number | null>(null);
  const max = Math.max(1, ...rows.map((r) => r.count));
  return (
    <ol className="flex flex-col gap-space-xs" aria-label="Application funnel">
      {rows.map((r, i) => {
        const w = (r.count / max) * 100;
        return (
          <li
            key={r.label}
            tabIndex={0}
            onMouseEnter={() => setHover(i)}
            onMouseLeave={() => setHover(null)}
            onFocus={() => setHover(i)}
            onBlur={() => setHover(null)}
            className="relative grid grid-cols-[minmax(0,11rem)_1fr] items-center gap-space-sm rounded-xs outline-none focus-visible:ring-1 focus-visible:ring-graphite sm:grid-cols-[minmax(0,14rem)_1fr]"
          >
            <span className="flex flex-col">
              <span className="text-body-sm text-on-surface">{r.label}</span>
              {r.note && <span className="font-mono text-label-sm text-muted">{r.note}</span>}
            </span>
            <span className="flex items-center gap-space-xs">
              <span className="relative h-5 flex-1">
                {r.count > 0 && (
                  <span
                    className="absolute inset-y-0 left-0 rounded-r-[4px]"
                    style={{ width: `max(${w}%, 4px)`, background: MARK, opacity: hover === null || hover === i ? 1 : 0.55 }}
                  />
                )}
              </span>
              <span className="w-10 text-right font-mono text-label-md text-on-surface">{r.count}</span>
            </span>
            {hover === i && (
              <Tip x="60%" y={0}>
                <strong className="font-semibold">{r.label}</strong>: {r.count}
                {r.note ? ` · ${r.note}` : ""}
              </Tip>
            )}
          </li>
        );
      })}
    </ol>
  );
}

function niceMax(v: number) {
  if (v <= 4) return 4;
  const pow = 10 ** Math.floor(Math.log10(v));
  for (const m of [1, 2, 2.5, 5, 10]) if (m * pow >= v) return m * pow;
  return 10 * pow;
}

const shortDate = (iso: string) => new Date(iso).toLocaleDateString(undefined, { day: "numeric", month: "short" });

/**
 * Small multiples: one column chart per measure, sharing the week axis and
 * one y-scale so heights compare across panels (never a dual axis).
 */
export function WeeklyColumns({ weeks, series }: { weeks: string[]; series: { label: string; values: number[] }[] }) {
  const max = niceMax(Math.max(1, ...series.flatMap((s) => s.values)));
  return (
    <div className="grid grid-cols-1 gap-space-md md:grid-cols-3">
      {series.map((s) => (
        <ColumnPanel key={s.label} label={s.label} weeks={weeks} values={s.values} max={max} />
      ))}
    </div>
  );
}

function ColumnPanel({ label, weeks, values, max }: { label: string; weeks: string[]; values: number[]; max: number }) {
  const [hover, setHover] = useState<number | null>(null);
  const W = 300, H = 140, padL = 24, padB = 18, padT = 8;
  const plotW = W - padL, plotH = H - padB - padT;
  const slot = plotW / values.length;
  const bar = Math.min(18, slot - 2);
  const y = (v: number) => padT + plotH - (v / max) * plotH;
  const last = values.length - 1;
  const total = values.reduce((a, b) => a + b, 0);
  return (
    <figure className="relative flex flex-col gap-1">
      <figcaption className="flex items-baseline justify-between">
        <span className="text-body-sm text-on-surface">{label}</span>
        <span className="font-mono text-label-sm text-muted">{total} in 12 weeks</span>
      </figcaption>
      <svg viewBox={`0 0 ${W} ${H}`} className="w-full" role="img" aria-label={`${label} per week, last 12 weeks: ${values.join(", ")}`}>
        {[0, max / 2, max].map((t) => (
          <g key={t}>
            <line x1={padL} x2={W} y1={y(t)} y2={y(t)} stroke={GRID} strokeWidth={1} />
            <text x={padL - 4} y={y(t) + 3} textAnchor="end" fontSize="9" fill="#6b6d65">
              {t}
            </text>
          </g>
        ))}
        {values.map((v, i) => {
          const x = padL + i * slot + (slot - bar) / 2;
          const h = Math.max(0, (v / max) * plotH);
          const r = Math.min(4, h);
          return (
            <g key={i}>
              {v > 0 && (
                // 4px rounded data end, square at the baseline.
                <path
                  d={`M${x},${y(0)} V${y(v) + r} Q${x},${y(v)} ${x + r},${y(v)} H${x + bar - r} Q${x + bar},${y(v)} ${x + bar},${y(v) + r} V${y(0)} Z`}
                  fill={MARK}
                  opacity={hover === null || hover === i ? 1 : 0.55}
                />
              )}
              <rect
                x={padL + i * slot}
                y={padT}
                width={slot}
                height={plotH}
                fill="transparent"
                tabIndex={0}
                aria-label={`Week of ${shortDate(weeks[i])}: ${v}`}
                onMouseEnter={() => setHover(i)}
                onMouseLeave={() => setHover(null)}
                onFocus={() => setHover(i)}
                onBlur={() => setHover(null)}
              />
            </g>
          );
        })}
        {values[last] > 0 && (
          <text x={padL + last * slot + slot / 2} y={y(values[last]) - 3} textAnchor="middle" fontSize="9" fill="#191d19">
            {values[last]}
          </text>
        )}
        <text x={padL} y={H - 4} fontSize="9" fill="#6b6d65">
          {shortDate(weeks[0])}
        </text>
        <text x={W} y={H - 4} fontSize="9" fill="#6b6d65" textAnchor="end">
          This week
        </text>
      </svg>
      {hover !== null && (
        <Tip x={`${((padL + hover * slot + slot / 2) / W) * 100}%`} y={20}>
          Week of {shortDate(weeks[hover])}: <strong className="font-semibold">{values[hover]}</strong>
        </Tip>
      )}
    </figure>
  );
}

/** Skills × weeks heatmap: share of strong matches missing each skill. */
export function SkillHeatmap({ weeks, rows }: { weeks: string[]; rows: { skill: string; total: number; share: number[] }[] }) {
  const [hover, setHover] = useState<{ r: number; c: number } | null>(null);
  const step = (v: number) => (v <= 0 ? 0 : Math.min(RAMP.length - 1, 1 + Math.floor(v * (RAMP.length - 1) * 0.999)));
  return (
    <div className="relative">
      <div className="overflow-x-auto">
        <table className="w-full min-w-[520px] border-separate" style={{ borderSpacing: 2 }}>
          <thead>
            <tr>
              <th className="text-left text-body-sm font-normal text-on-surface-variant">Skill (jobs)</th>
              {weeks.map((w, i) => (
                <th key={w} className="font-mono text-label-sm font-normal text-muted">
                  {i === weeks.length - 1 ? "This wk" : shortDate(w)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((r, ri) => (
              <tr key={r.skill}>
                <th scope="row" className="pr-space-sm text-left text-body-sm font-normal text-on-surface">
                  {r.skill} <span className="font-mono text-label-sm text-muted">({r.total})</span>
                </th>
                {r.share.map((v, ci) => (
                  <td
                    key={ci}
                    tabIndex={0}
                    aria-label={`${r.skill}, week of ${shortDate(weeks[ci])}: ${Math.round(v * 100)}% of strong matches`}
                    onMouseEnter={() => setHover({ r: ri, c: ci })}
                    onMouseLeave={() => setHover(null)}
                    onFocus={() => setHover({ r: ri, c: ci })}
                    onBlur={() => setHover(null)}
                    className="h-7 rounded-[4px] outline-none focus-visible:ring-1 focus-visible:ring-graphite"
                    style={{ background: RAMP[step(v)] }}
                  />
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="mt-space-xs flex items-center gap-space-xs text-body-sm text-on-surface-variant">
        <span>0%</span>
        {RAMP.map((c) => (
          <span key={c} className="h-3 w-6 rounded-[2px]" style={{ background: c }} aria-hidden />
        ))}
        <span>100% of strong matches that week</span>
      </div>
      {hover && (
        <div role="status" className="mt-1 text-body-sm text-on-surface">
          {rows[hover.r].skill}, week of {shortDate(weeks[hover.c])}: <strong className="font-semibold">{Math.round(rows[hover.r].share[hover.c] * 100)}%</strong> of
          strong matches list it as missing.
        </div>
      )}
    </div>
  );
}
