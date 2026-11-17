import clsx from "clsx";
import { Button } from "../../components/ui/Button";
import { Icon } from "../../components/ui/Icon";
import { editableTypes, newBlockId, type Block, type Content, type FactRef, type Issue, type Section } from "../../lib/preparation";

const factKindLabel: Record<string, string> = {
  basics: "Profile",
  experience: "Role",
  achievement: "Achievement",
  education: "Education",
  skill: "Skill",
  certification: "Certification",
  project: "Project",
  note: "Your note",
};

function Evidence({ refs, facts }: { refs: string[]; facts: Map<string, FactRef> }) {
  if (refs.length === 0) return null;
  return (
    <details className="mt-1 text-body-sm">
      <summary className="cursor-pointer font-mono text-label-sm text-muted">
        Cites {refs.length} profile {refs.length === 1 ? "fact" : "facts"}
      </summary>
      <ul className="mt-1 flex flex-col gap-1 border-l-2 border-stone pl-space-sm">
        {refs.map((r) => {
          const f = facts.get(r);
          return (
            <li key={r} className={f ? "text-on-surface-variant" : "text-terracotta"}>
              {f ? (
                <>
                  <span className="label-caps mr-1 text-outline">{factKindLabel[f.kind] ?? f.kind}</span>
                  {f.text}
                </>
              ) : (
                "A cited fact is no longer in your verified profile."
              )}
            </li>
          );
        })}
      </ul>
    </details>
  );
}

function IssueList({ issues }: { issues: Issue[] }) {
  if (issues.length === 0) return null;
  return (
    <ul className="mt-1 flex flex-col gap-0.5">
      {issues.map((i, n) => (
        <li key={n} className={clsx("flex items-start gap-1 text-body-sm", i.severity === "ERROR" ? "text-terracotta" : "text-olive")}>
          <Icon name={i.severity === "ERROR" ? "error" : "info"} size={14} className="mt-0.5 shrink-0" />
          <span>{i.message}</span>
        </li>
      ))}
    </ul>
  );
}

type Props = {
  content: Content;
  onChange: (c: Content) => void;
  issuesByBlock: Map<string, Issue[]>;
  facts: Map<string, FactRef>;
  readOnly?: boolean;
};

/** Block-level editor: the user writes claims; structure comes from the profile. */
export function BlockEditor({ content, onChange, issuesByBlock, facts, readOnly }: Props) {
  const update = (si: number, fn: (s: Section) => Section) =>
    onChange({ ...content, sections: content.sections.map((s, i) => (i === si ? fn(s) : s)) });
  const setText = (si: number, id: string, text: string) =>
    update(si, (s) => ({ ...s, blocks: s.blocks.map((b) => (b.id === id ? { ...b, text } : b)) }));
  const remove = (si: number, id: string) =>
    update(si, (s) => ({ ...s, blocks: s.blocks.filter((b) => b.id !== id && b.meta.parent !== id) }));
  const addAfter = (si: number, after: Block | null, block: Block) =>
    update(si, (s) => {
      if (!after) return { ...s, blocks: [...s.blocks, block] };
      // Insert after the entry's last child bullet.
      let idx = s.blocks.findIndex((b) => b.id === after.id);
      while (idx + 1 < s.blocks.length && s.blocks[idx + 1].meta.parent === after.id) idx++;
      return { ...s, blocks: [...s.blocks.slice(0, idx + 1), block, ...s.blocks.slice(idx + 1)] };
    });

  return (
    <div className="flex flex-col">
      {content.sections.map((s, si) => (
        <section key={s.key} className="border-b border-stone p-space-md last:border-b-0" aria-label={s.title}>
          {s.key !== "contact" && (
            <h2 className={clsx("mb-space-sm", content.kind === "APPLICATION_ANSWERS" ? "text-body-md font-semibold text-on-surface" : "label-caps text-outline")}>
              {s.title}
            </h2>
          )}
          <div className="flex flex-col gap-space-sm">
            {s.blocks.map((b) => {
              const issues = issuesByBlock.get(b.id) ?? [];
              const hasError = issues.some((i) => i.severity === "ERROR");
              if (b.type === "contact") {
                return (
                  <div key={b.id}>
                    <p className="font-serif text-headline-md text-on-surface">{b.text}</p>
                    <p className="text-body-sm text-on-surface-variant">{[b.meta.email, b.meta.location, b.meta.links].filter(Boolean).join(" · ")}</p>
                    <p className="font-mono text-label-sm text-muted">From your account and profile</p>
                  </div>
                );
              }
              if (b.type === "entry" || b.type === "skills" || b.type === "heading") {
                return (
                  <div key={b.id} className={clsx("group", b.type === "entry" && "mt-space-xs")}>
                    <div className="flex flex-wrap items-baseline justify-between gap-space-sm">
                      <p className={clsx(b.type === "entry" ? "font-semibold text-on-surface" : "text-body-md text-on-surface")}>
                        {b.text || <span className="text-terracotta">Missing profile fact</span>}
                      </p>
                      <span className="font-mono text-label-sm text-muted">{[b.meta.location, b.meta.dates].filter(Boolean).join(" · ")}</span>
                    </div>
                    <div className="flex items-center gap-space-sm">
                      <span className="font-mono text-label-sm text-muted">From profile · edit it there</span>
                      {!readOnly && b.type === "entry" && s.key !== "education" && s.key !== "certifications" && (
                        <button
                          type="button"
                          className="font-mono text-label-sm text-outline underline hover:text-on-surface"
                          onClick={() => addAfter(si, b, { id: newBlockId(), type: "bullet", text: "", refs: [...b.refs], meta: { parent: b.id } })}
                        >
                          + bullet
                        </button>
                      )}
                      {!readOnly && (
                        <button type="button" className="font-mono text-label-sm text-outline underline hover:text-terracotta" onClick={() => remove(si, b.id)}>
                          remove
                        </button>
                      )}
                    </div>
                    <IssueList issues={issues} />
                  </div>
                );
              }
              // Claims: paragraph, bullet, answer.
              const label = b.meta.role === "greeting" ? "Greeting" : b.meta.role === "closing" ? "Sign-off" : b.type === "answer" ? "Answer" : b.type === "bullet" ? "Bullet" : "Paragraph";
              return (
                <div key={b.id} className={clsx(b.type === "bullet" && "pl-space-md")}>
                  {b.meta.status === "NEEDS_INPUT" && (
                    <p className="mb-1 flex items-center gap-1 text-body-sm text-terracotta">
                      <Icon name="edit_note" size={16} /> {b.meta.question_for_user || "Your input is needed here."}
                    </p>
                  )}
                  <div className="flex items-start gap-space-xs">
                    {b.type === "bullet" && <span aria-hidden className="mt-2 text-outline">•</span>}
                    {readOnly || !editableTypes.has(b.type) ? (
                      <p className="whitespace-pre-wrap text-body-md text-on-surface">{b.text}</p>
                    ) : (
                      <textarea
                        aria-label={label}
                        value={b.text}
                        rows={Math.max(b.type === "bullet" ? 1 : 3, Math.ceil(b.text.length / 90))}
                        maxLength={5000}
                        onChange={(e) => setText(si, b.id, e.target.value)}
                        className={clsx(
                          "w-full resize-y rounded-sm border bg-surface-container-lowest px-2 py-1 text-body-md text-on-surface focus:border-graphite focus:outline-none",
                          hasError ? "border-terracotta" : "border-stone",
                        )}
                      />
                    )}
                    {!readOnly && b.type !== "answer" && !b.meta.role && (
                      <button type="button" aria-label={`Remove ${label.toLowerCase()}`} onClick={() => remove(si, b.id)} className="mt-1 text-outline hover:text-terracotta">
                        <Icon name="close" size={16} />
                      </button>
                    )}
                  </div>
                  <IssueList issues={issues} />
                  <Evidence refs={b.refs} facts={facts} />
                </div>
              );
            })}
            {!readOnly && (s.key === "summary" || s.key === "letter") && (
              <Button
                variant="ghost"
                icon="add"
                className="self-start py-1 text-[13px]"
                onClick={() => {
                  const closing = s.blocks.find((b) => b.meta.role === "closing");
                  const block: Block = { id: newBlockId(), type: "paragraph", text: "", refs: [], meta: {} };
                  if (closing) {
                    update(si, (sec) => {
                      const i = sec.blocks.indexOf(closing);
                      return { ...sec, blocks: [...sec.blocks.slice(0, i), block, ...sec.blocks.slice(i)] };
                    });
                  } else addAfter(si, null, block);
                }}
              >
                Add paragraph
              </Button>
            )}
          </div>
        </section>
      ))}
    </div>
  );
}
