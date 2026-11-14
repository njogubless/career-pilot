import { useState } from "react";
import { apiFieldError } from "../../lib/api";
import { Button } from "../../components/ui/Button";
import { FormError } from "../../components/ui/Form";
import { Icon } from "../../components/ui/Icon";
import { EmptyState, Panel } from "../../components/ui/Panel";
import type { Skill } from "../../lib/profile";
import { profileApi, useProfileMutation } from "../../lib/profile";

/** Skills as compact chips; imported skills awaiting review are marked terracotta. */
export function SkillsPanel({ skills }: { skills: Skill[] }) {
  const [name, setName] = useState("");
  const add = useProfileMutation((n: string) => profileApi.create("skills", { name: n }));
  const verify = useProfileMutation((id: string) => profileApi.verify("skills", id, "VERIFIED"));
  const remove = useProfileMutation((id: string) => profileApi.remove("skills", id));
  const pending = skills.filter((s) => s.verification === "UNVERIFIED");
  const active = skills.filter((s) => s.verification !== "REJECTED");

  return (
    <Panel>
      <div className="p-space-md">
        <p className="label-caps text-outline">Competency</p>
        <h2 className="font-serif text-headline-sm text-on-surface">Skills</h2>
      </div>
      <form
        className="flex gap-space-sm border-t border-stone p-space-md"
        onSubmit={(e) => {
          e.preventDefault();
          if (name.trim()) add.mutate(name.trim(), { onSuccess: () => setName("") });
        }}
      >
        <label htmlFor="new-skill" className="sr-only">
          Add a skill
        </label>
        <input
          id="new-skill"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Add a skill, e.g. PostgreSQL"
          maxLength={100}
          className="w-full rounded-sm border border-stone bg-surface-container-lowest px-3 py-2 text-body-md focus:border-graphite focus:outline-none"
        />
        <Button type="submit" variant="secondary" icon="add" disabled={add.isPending || !name.trim()}>
          Add
        </Button>
      </form>
      {add.error ? (
        <div className="px-space-md pb-space-sm">
          <FormError error={apiFieldError(add.error, "name") ? new Error(apiFieldError(add.error, "name")) : add.error} />
        </div>
      ) : null}
      {pending.length > 0 && (
        <div className="flex items-center justify-between gap-space-sm border-t border-stone bg-tertiary-fixed/30 px-space-md py-space-sm">
          <span className="text-body-sm text-on-surface">{pending.length} imported skill(s) need review.</span>
          <Button
            variant="secondary"
            className="px-2 py-1 text-[12px]"
            disabled={verify.isPending}
            onClick={() => pending.forEach((s) => verify.mutate(s.id))}
          >
            Confirm all listed
          </Button>
        </div>
      )}
      {active.length === 0 ? (
        <EmptyState icon="construction" title="No skills yet">
          Skills carry 35% of the match score. Add them here or import them from your CV.
        </EmptyState>
      ) : (
        <ul className="flex flex-wrap gap-space-xs border-t border-stone p-space-md" aria-label="Skills">
          {active.map((s) => (
            <li
              key={s.id}
              className={
                "inline-flex items-center gap-1 rounded-xs border px-2 py-1 font-mono text-label-md " +
                (s.verification === "UNVERIFIED" ? "border-terracotta text-terracotta" : "border-stone text-on-surface")
              }
            >
              {s.name}
              {s.verification === "UNVERIFIED" && (
                <button type="button" onClick={() => verify.mutate(s.id)} className="text-olive hover:text-graphite" aria-label={`Confirm ${s.name}`}>
                  <Icon name="check" size={14} />
                </button>
              )}
              <button
                type="button"
                onClick={() => remove.mutate(s.id)}
                className="text-outline hover:text-terracotta"
                aria-label={`Remove ${s.name}`}
              >
                <Icon name="close" size={14} />
              </button>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
}
