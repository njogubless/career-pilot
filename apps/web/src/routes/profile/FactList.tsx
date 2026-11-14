import { useState, type ReactNode } from "react";
import { Button } from "../../components/ui/Button";
import { EmptyState, Panel } from "../../components/ui/Panel";
import { ProvenanceBadge } from "../../components/ui/Provenance";
import type { FactKind, Source, Verification } from "../../lib/profile";
import { profileApi, useProfileMutation } from "../../lib/profile";

type Fact = { id: string; source: Source; verification: Verification };

type Props<T extends Fact> = {
  kind: FactKind;
  title: string;
  eyebrow: string;
  icon: string;
  emptyText: string;
  items: T[];
  render: (item: T) => ReactNode;
  form: (props: { item?: T; onClose: () => void }) => ReactNode;
};

/** A profile section of facts with provenance, verification and editing. */
export function FactList<T extends Fact>({ kind, title, eyebrow, icon, emptyText, items, render, form }: Props<T>) {
  const [editing, setEditing] = useState<T | "new" | null>(null);
  const verify = useProfileMutation((v: { id: string; verification: Verification }) => profileApi.verify(kind, v.id, v.verification));
  const remove = useProfileMutation((id: string) => profileApi.remove(kind, id));

  return (
    <Panel>
      <div className="flex items-end justify-between gap-space-sm p-space-md">
        <div>
          <p className="label-caps text-outline">{eyebrow}</p>
          <h2 className="font-serif text-headline-sm text-on-surface">{title}</h2>
        </div>
        <Button variant="secondary" icon="add" onClick={() => setEditing("new")}>
          Add
        </Button>
      </div>
      {items.length === 0 ? (
        <EmptyState icon={icon} title={`No ${title.toLowerCase()} yet`}>
          {emptyText}
        </EmptyState>
      ) : (
        <ul className="divide-y divide-stone border-t border-stone">
          {items.map((item) => (
            <li key={item.id} className={item.verification === "REJECTED" ? "opacity-60" : undefined}>
              <div className="flex flex-col gap-space-sm p-space-md sm:flex-row sm:items-start sm:justify-between">
                <div className="min-w-0 flex-1">{render(item)}</div>
                <div className="flex shrink-0 flex-col items-start gap-space-sm sm:items-end">
                  <ProvenanceBadge source={item.source} verification={item.verification} />
                  <div className="flex flex-wrap gap-1">
                    {item.verification !== "VERIFIED" && (
                      <Button
                        variant="secondary"
                        className="px-2 py-1 text-[12px]"
                        disabled={verify.isPending}
                        onClick={() => verify.mutate({ id: item.id, verification: "VERIFIED" })}
                      >
                        Confirm accurate
                      </Button>
                    )}
                    {item.verification === "UNVERIFIED" && (
                      <Button
                        variant="ghost"
                        className="px-2 py-1 text-[12px]"
                        disabled={verify.isPending}
                        onClick={() => verify.mutate({ id: item.id, verification: "REJECTED" })}
                      >
                        Reject
                      </Button>
                    )}
                    <Button variant="ghost" className="px-2 py-1 text-[12px]" onClick={() => setEditing(item)}>
                      Edit
                    </Button>
                    <Button
                      variant="ghost"
                      className="px-2 py-1 text-[12px] text-terracotta"
                      disabled={remove.isPending}
                      onClick={() => {
                        if (window.confirm("Delete this entry? This cannot be undone.")) remove.mutate(item.id);
                      }}
                    >
                      Delete
                    </Button>
                  </div>
                </div>
              </div>
            </li>
          ))}
        </ul>
      )}
      {editing !== null && form({ item: editing === "new" ? undefined : editing, onClose: () => setEditing(null) })}
    </Panel>
  );
}

export function Dates({ start, end, current }: { start: string | null; end: string | null; current?: boolean }) {
  const fmt = (d: string | null) => (d ? new Date(d + "T00:00:00Z").toLocaleDateString(undefined, { month: "short", year: "numeric", timeZone: "UTC" }) : "");
  if (!start && !end && !current) return null;
  return (
    <span className="font-mono text-label-md text-muted">
      {fmt(start) || "?"} – {current ? "Present" : fmt(end) || "?"}
    </span>
  );
}
