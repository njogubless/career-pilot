import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Button } from "../../components/ui/Button";
import { FormError } from "../../components/ui/Form";
import { Panel } from "../../components/ui/Panel";
import { Tag } from "../../components/ui/Tag";
import { api } from "../../lib/api";
import type { Extraction } from "../../lib/documents";
import { buildImport, initChoices, type Choice, type Section } from "../../lib/extractionImport";
import { profileKey, type Basics } from "../../lib/profile";

type Result = { created: number; verified: number; skipped_duplicate_skills: number };

export function ExtractionReview({ extraction, basics, onDone }: { extraction: Extraction; basics: Basics; onDone: () => void }) {
  const p = extraction.proposal!;
  const [choices, setChoices] = useState(() => initChoices(p));
  const [applyBasics, setApplyBasics] = useState(true);
  const qc = useQueryClient();
  const apply = useMutation({
    mutationFn: () => api.post<Result>(`/api/v1/profile-extractions/${extraction.id}/apply`, buildImport(p, choices, applyBasics, basics)),
    onSuccess: () => qc.invalidateQueries({ queryKey: profileKey }),
  });
  const discard = useMutation({ mutationFn: () => api.post<void>(`/api/v1/profile-extractions/${extraction.id}/discard`), onSuccess: onDone });

  const set = (s: Section, i: number, patch: Partial<Choice>) =>
    setChoices((c) => ({ ...c, [s]: c[s].map((x, j) => (j === i ? { ...x, ...patch, ...(patch.accept === false ? { verified: false } : {}) } : x)) }));
  const setAll = (verified: boolean) =>
    setChoices((c) => Object.fromEntries(Object.entries(c).map(([k, v]) => [k, v.map((x) => ({ ...x, verified: x.accept && verified }))])) as typeof c);

  if (apply.isSuccess) {
    return (
      <Panel className="p-space-md">
        <p role="status" className="text-body-md text-on-surface">
          Added {apply.data.created} item(s) to your profile ({apply.data.verified} confirmed as accurate
          {apply.data.skipped_duplicate_skills ? `, ${apply.data.skipped_duplicate_skills} duplicate skill(s) skipped` : ""}). Items you did not
          confirm are marked “Needs review” on your profile.
        </p>
        <div className="mt-space-sm">
          <Button variant="secondary" onClick={onDone}>
            Done
          </Button>
        </div>
      </Panel>
    );
  }

  const row = (s: Section, i: number, title: string, sub: string | null, found: boolean, evidence: string) => (
    <li key={`${s}-${i}`} className="flex flex-col gap-space-xs p-space-md sm:flex-row sm:items-start sm:justify-between">
      <div className="min-w-0">
        <p className="text-title-md text-on-surface">{title}</p>
        {sub && <p className="text-body-sm text-on-surface-variant">{sub}</p>}
        <p className="mt-1 text-body-sm text-muted">
          <span className="label-caps mr-1">Evidence</span>“{evidence}”
        </p>
        {!found && <Tag tone="terracotta" className="mt-1">Not found in your CV</Tag>}
      </div>
      <div className="flex shrink-0 flex-col gap-1 text-body-sm">
        <label className="inline-flex items-center gap-2">
          <input type="checkbox" className="h-4 w-4 accent-graphite" checked={choices[s][i].accept} onChange={(e) => set(s, i, { accept: e.target.checked })} />
          Add to profile
        </label>
        <label className="inline-flex items-center gap-2">
          <input
            type="checkbox"
            className="h-4 w-4 accent-graphite"
            disabled={!choices[s][i].accept}
            checked={choices[s][i].verified}
            onChange={(e) => set(s, i, { verified: e.target.checked })}
          />
          I confirm this is accurate
        </label>
      </div>
    </li>
  );

  const group = (label: string, s: Section, items: { title: string; sub: string | null; found: boolean; evidence: string }[]) =>
    items.length === 0 ? null : (
      <section aria-label={label}>
        <h3 className="label-caps border-t border-stone bg-surface-container-low px-space-md py-space-xs text-outline">
          {label} ({items.length})
        </h3>
        <ul className="divide-y divide-stone">{items.map((it, i) => row(s, i, it.title, it.sub, it.found, it.evidence))}</ul>
      </section>
    );

  return (
    <Panel>
      <div className="flex flex-col gap-space-sm p-space-md">
        <div className="flex flex-wrap items-center justify-between gap-space-sm">
          <div>
            <p className="label-caps text-outline">Review extraction</p>
            <h2 className="font-serif text-headline-sm text-on-surface">Check what was found in your CV</h2>
          </div>
          <span className="font-mono text-label-sm text-muted">
            {extraction.provider} · {extraction.model} · {extraction.prompt_version}
          </span>
        </div>
        <p className="text-body-sm text-on-surface-variant">
          Nothing is saved until you apply. Tick “I confirm this is accurate” only for items you have checked; unconfirmed items are saved as
          “Needs review” and are not used in generated applications.
        </p>
        {p.warnings.length > 0 && (
          <ul className="space-y-1 border-l-2 border-terracotta pl-space-sm text-body-sm text-on-surface">
            {p.warnings.map((w) => (
              <li key={w}>{w}</li>
            ))}
          </ul>
        )}
      </div>

      <section aria-label="Personal details" className="border-t border-stone p-space-md">
        <label className="inline-flex items-center gap-2 text-body-md">
          <input type="checkbox" className="h-4 w-4 accent-graphite" checked={applyBasics} onChange={(e) => setApplyBasics(e.target.checked)} />
          Update personal details
        </label>
        <dl className="mt-space-sm grid gap-x-space-md gap-y-1 text-body-sm sm:grid-cols-[auto_1fr]">
          {(["headline", "location_city", "location_country"] as const).map((k) =>
            p.personal[k] ? (
              <div key={k} className="contents">
                <dt className="label-caps text-outline">{k.replace("_", " ")}</dt>
                <dd className="text-on-surface">{p.personal[k]}</dd>
              </div>
            ) : null,
          )}
          {p.personal.links.length > 0 && (
            <div className="contents">
              <dt className="label-caps text-outline">links</dt>
              <dd className="break-all text-on-surface">{p.personal.links.map((l) => l.url).join(", ")}</dd>
            </div>
          )}
        </dl>
      </section>

      {group("Experience", "experiences", p.experiences.map((e) => ({ title: `${e.title} · ${e.employer}`, sub: [e.start_date, e.is_current ? "Present" : e.end_date].filter(Boolean).join(" – ") || null, found: e.evidence_found, evidence: e.evidence })))}
      {group("Education", "education", p.education.map((e) => ({ title: e.institution, sub: [e.degree, e.field].filter(Boolean).join(", ") || null, found: e.evidence_found, evidence: e.evidence })))}
      {group("Skills", "skills", p.skills.map((s) => ({ title: s.name, sub: s.category, found: s.evidence_found, evidence: s.evidence })))}
      {group("Certifications", "certifications", p.certifications.map((c) => ({ title: c.name, sub: c.issuer, found: c.evidence_found, evidence: c.evidence })))}
      {group("Projects", "projects", p.projects.map((pr) => ({ title: pr.name, sub: pr.description, found: pr.evidence_found, evidence: pr.evidence })))}

      <div className="flex flex-col gap-space-sm border-t border-stone p-space-md">
        <FormError error={apply.error ?? discard.error} />
        <div className="flex flex-wrap items-center justify-between gap-space-sm">
          <div className="flex gap-space-sm">
            <Button variant="ghost" onClick={() => setAll(true)}>
              Confirm all selected
            </Button>
            <Button variant="ghost" onClick={() => setAll(false)}>
              Clear confirmations
            </Button>
          </div>
          <div className="flex gap-space-sm">
            <Button variant="secondary" disabled={discard.isPending} onClick={() => discard.mutate()}>
              Discard
            </Button>
            <Button variant="accent" disabled={apply.isPending} onClick={() => apply.mutate()}>
              {apply.isPending ? "Applying…" : "Apply to profile"}
            </Button>
          </div>
        </div>
      </div>
    </Panel>
  );
}
