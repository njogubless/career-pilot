import { useState } from "react";
import { useSearchParams } from "react-router";
import { Icon } from "../../components/ui/Icon";

const selectCls =
  "rounded-xs border border-stone bg-surface-container-lowest px-2 py-1 font-mono text-label-md text-on-surface focus:border-graphite focus:outline-none";

/** Query bar + filter chips. All state lives in the URL (shareable, savable). */
export function FilterBar({ total }: { total: number | undefined }) {
  const [params, setParams] = useSearchParams();
  const urlQ = params.get("q") ?? "";
  const [q, setQ] = useState(urlQ);
  const [syncedQ, setSyncedQ] = useState(urlQ);
  if (syncedQ !== urlQ) {
    // URL changed (navigation, saved search): adopt it without an effect.
    setSyncedQ(urlQ);
    setQ(urlQ);
  }

  const set = (key: string, value: string | null) => {
    const next = new URLSearchParams(params);
    if (value) next.set(key, value);
    else next.delete(key);
    next.delete("cursor");
    setParams(next);
  };

  const chip = (label: string, key: string, options: [string, string][]) => (
    <label className="inline-flex items-center gap-1">
      <span className="label-caps text-outline">{label}</span>
      <select aria-label={label} className={selectCls} value={params.get(key) ?? ""} onChange={(e) => set(key, e.target.value || null)}>
        <option value="">Any</option>
        {options.map(([v, l]) => (
          <option key={v} value={v}>
            {l}
          </option>
        ))}
      </select>
    </label>
  );

  return (
    <section aria-label="Search and filters" className="rounded-sm border border-stone bg-surface-container-lowest">
      <form
        role="search"
        className="flex items-center gap-space-sm border-b border-stone p-space-sm"
        onSubmit={(e) => {
          e.preventDefault();
          set("q", q.trim() || null);
        }}
      >
        <Icon name="terminal" className="text-outline" />
        <label htmlFor="job-q" className="sr-only">
          Search jobs
        </label>
        <input
          id="job-q"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          maxLength={200}
          placeholder="Go, Kubernetes, data platform…"
          className="w-full bg-transparent font-mono text-label-md text-on-surface placeholder:text-outline focus:outline-none"
        />
        <span className="label-caps hidden shrink-0 text-outline sm:inline">Filter match:</span>
        <span className="label-caps shrink-0 rounded-xs bg-citron px-1.5 py-0.5 text-graphite">{total === undefined ? "…" : `${total} jobs`}</span>
      </form>
      <div className="flex flex-wrap items-center gap-x-space-md gap-y-space-sm p-space-sm">
        {chip("Work", "remote", [
          ["REMOTE", "Remote"],
          ["HYBRID", "Hybrid"],
          ["ONSITE", "On-site"],
        ])}
        {chip("Source", "source", [
          ["GREENHOUSE", "Greenhouse"],
          ["LEVER", "Lever"],
          ["ASHBY", "Ashby"],
          ["REMOTEOK", "Remote OK"],
        ])}
        {chip("Type", "employment", [
          ["FULL_TIME", "Full-time"],
          ["CONTRACT", "Contract"],
          ["PART_TIME", "Part-time"],
          ["INTERNSHIP", "Internship"],
        ])}
        {chip("Posted", "posted_within", [
          ["1", "24 hours"],
          ["7", "7 days"],
          ["30", "30 days"],
        ])}
        <label className="inline-flex items-center gap-1">
          <span className="label-caps text-outline">Open to</span>
          <input
            aria-label="Country (two-letter code)"
            className={selectCls + " w-14 uppercase"}
            maxLength={2}
            placeholder="KE"
            defaultValue={params.get("country") ?? ""}
            key={params.get("country") ?? ""}
            onBlur={(e) => {
              const v = e.target.value.trim().toUpperCase();
              set("country", /^[A-Z]{2}$/.test(v) ? v : null);
            }}
          />
        </label>
        <label className="inline-flex items-center gap-1 font-mono text-label-md text-on-surface">
          <input
            type="checkbox"
            className="h-3.5 w-3.5 accent-graphite"
            checked={params.get("salary_disclosed") === "true"}
            onChange={(e) => set("salary_disclosed", e.target.checked ? "true" : null)}
          />
          Salary disclosed
        </label>
        <label className="ml-auto inline-flex items-center gap-1">
          <span className="label-caps text-outline">Sort</span>
          <select
            aria-label="Sort"
            className={selectCls}
            value={params.get("sort") ?? (params.get("q") ? "relevance" : "newest")}
            onChange={(e) => set("sort", e.target.value)}
          >
            <option value="newest">Newest</option>
            <option value="relevance" disabled={!params.get("q")}>
              Relevance
            </option>
            <option value="salary">Highest disclosed salary</option>
          </select>
        </label>
      </div>
    </section>
  );
}
