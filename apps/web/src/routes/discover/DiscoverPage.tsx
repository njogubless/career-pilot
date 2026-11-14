import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useNavigate, useSearchParams } from "react-router";
import { Button } from "../../components/ui/Button";
import { FormError } from "../../components/ui/Form";
import { Loading } from "../../components/ui/Loading";
import { EmptyState, Panel } from "../../components/ui/Panel";
import { Tag } from "../../components/ui/Tag";
import { api } from "../../lib/api";
import { filterQuery, savedSearchQuery, useJobs, useSavedSearches, useSources, type SavedSearch } from "../../lib/jobs";
import { FilterBar } from "./FilterBar";
import { IngestionPanel } from "./IngestionPanel";
import { JobCard } from "./JobCard";

function SavedSearches({ query }: { query: string }) {
  const saved = useSavedSearches();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const save = useMutation({
    mutationFn: () => api.post<SavedSearch>("/api/v1/saved-searches", { name: name.trim(), query }),
    onSuccess: () => {
      setName("");
      void qc.invalidateQueries({ queryKey: ["saved-searches"] });
    },
  });
  const remove = useMutation({
    mutationFn: (id: string) => api.del(`/api/v1/saved-searches/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["saved-searches"] }),
  });

  return (
    <Panel className="p-space-md">
      <p className="label-caps text-outline">Saved searches</p>
      <ul className="mt-space-xs flex flex-wrap gap-1">
        {saved.data?.map((s) => (
          <li key={s.id} className="inline-flex items-center gap-1 rounded-xs border border-stone px-2 py-0.5 font-mono text-label-md">
            <button type="button" className="hover:underline" onClick={() => navigate(`/discover?${savedSearchQuery(s.filters)}`)}>
              {s.name}
            </button>
            <button type="button" aria-label={`Delete saved search ${s.name}`} className="text-outline hover:text-terracotta" onClick={() => remove.mutate(s.id)}>
              ×
            </button>
          </li>
        ))}
        {saved.data?.length === 0 && <li className="text-body-sm text-muted">None yet. Save the current filters below.</li>}
      </ul>
      <form
        className="mt-space-sm flex gap-space-xs"
        onSubmit={(e) => {
          e.preventDefault();
          if (name.trim()) save.mutate();
        }}
      >
        <label htmlFor="saved-name" className="sr-only">
          Saved search name
        </label>
        <input
          id="saved-name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          maxLength={80}
          placeholder="Name this search"
          className="w-full rounded-sm border border-stone px-2 py-1 text-body-sm focus:border-graphite focus:outline-none"
        />
        <Button type="submit" variant="secondary" className="px-2 py-1 text-[12px]" disabled={!name.trim() || save.isPending}>
          Save
        </Button>
      </form>
      <div className="mt-1">
        <FormError error={save.error} />
      </div>
    </Panel>
  );
}

export function DiscoverPage() {
  const [params] = useSearchParams();
  const query = filterQuery(params);
  const [cursors, setCursors] = useState<(string | null)[]>([null]);
  const [lastQuery, setLastQuery] = useState(query);
  if (lastQuery !== query) {
    setLastQuery(query);
    setCursors([null]);
  }
  const cursor = cursors[cursors.length - 1];
  const jobs = useJobs(query, cursor);
  const sources = useSources();
  const kinds = [...new Set(sources.data?.map((s) => s.kind))];

  return (
    <div className="flex flex-col gap-space-lg">
      <header className="flex flex-col gap-space-xs">
        <div className="flex flex-wrap items-center gap-space-xs">
          <Tag tone="graphite">Index // Active feed</Tag>
          {sources.data && <span className="label-caps text-olive">{sources.data.length} public job boards indexed</span>}
        </div>
        <h1 className="font-serif text-headline-lg-mobile text-on-surface md:text-display-lg">Opportunity Intelligence Directory</h1>
        <p className="max-w-3xl text-body-lg text-on-surface-variant">
          Live index of approved public job boards{kinds.length ? ` (${kinds.map((k) => ({ GREENHOUSE: "Greenhouse", LEVER: "Lever", ASHBY: "Ashby", REMOTEOK: "Remote OK" })[k]).join(", ")})` : ""}.
          Duplicates across sources are merged and expired postings removed. Match scores against your profile arrive in Phase 4.
        </p>
      </header>

      <FilterBar total={jobs.data?.total} />

      <div className="grid grid-cols-1 items-start gap-space-lg xl:grid-cols-12">
        <section aria-label="Results" className="flex min-w-0 flex-col gap-space-sm xl:col-span-8">
          {jobs.isPending && <Loading label="Searching the index" />}
          {jobs.isError && (
            <p role="alert" className="text-terracotta">
              {jobs.error.message}
            </p>
          )}
          {jobs.data?.data.length === 0 && (
            <Panel>
              <EmptyState icon="travel_explore" title={query ? "No jobs match these filters" : "The index is empty"}>
                {query
                  ? "Try removing a filter. Jobs with unknown details (for example salary) are kept unless you explicitly filter them out."
                  : "Run a discovery cycle to fetch postings from the configured job boards. The first sync can take a few minutes."}
              </EmptyState>
            </Panel>
          )}
          {jobs.data?.data.map((j) => (
            <JobCard key={j.id} job={j} />
          ))}
          {jobs.data && jobs.data.total > 0 && (
            <nav aria-label="Pagination" className="flex items-center justify-between rounded-sm border border-stone bg-surface-container-lowest p-space-sm">
              <span className="text-body-sm text-on-surface-variant">
                Showing {(cursors.length - 1) * 20 + 1}–{(cursors.length - 1) * 20 + jobs.data.data.length} of {jobs.data.total}
              </span>
              <div className="flex gap-1">
                <Button variant="ghost" className="font-mono text-label-md" disabled={cursors.length === 1} onClick={() => setCursors((c) => c.slice(0, -1))}>
                  Prev
                </Button>
                <Button
                  variant="ghost"
                  className="font-mono text-label-md"
                  disabled={!jobs.data.next_cursor}
                  onClick={() => jobs.data?.next_cursor && setCursors((c) => [...c, jobs.data!.next_cursor])}
                >
                  Next
                </Button>
              </div>
            </nav>
          )}
        </section>
        <aside className="flex flex-col gap-space-lg xl:sticky xl:top-20 xl:col-span-4">
          <SavedSearches query={query} />
          <IngestionPanel />
        </aside>
      </div>
    </div>
  );
}
