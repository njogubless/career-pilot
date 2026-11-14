import { zodResolver } from "@hookform/resolvers/zod";
import { apiFieldError } from "../../lib/api";
import { useForm } from "react-hook-form";
import { z } from "zod";
import { Button } from "../../components/ui/Button";
import { Checkbox, FormError, Select, TextInput } from "../../components/ui/Form";
import { Panel } from "../../components/ui/Panel";
import type { Preferences } from "../../lib/profile";
import { profileApi, useProfileMutation } from "../../lib/profile";

const splitList = (s: string) =>
  s
    .split(",")
    .map((x) => x.trim())
    .filter(Boolean);

const EMPLOYMENT = ["FULL_TIME", "PART_TIME", "CONTRACT", "INTERNSHIP", "TEMPORARY"] as const;
const SENIORITY = ["INTERN", "JUNIOR", "MID", "SENIOR", "STAFF", "PRINCIPAL", "LEAD", "MANAGER", "DIRECTOR", "EXECUTIVE"] as const;
const label = (s: string) => s.charAt(0) + s.slice(1).toLowerCase().replace("_", "-");

const schema = z
  .object({
    target_roles: z.string(),
    industries: z.string(),
    preferred_locations: z.string(),
    work_eligible_countries: z
      .string()
      .refine((v) => splitList(v).every((c) => /^[A-Za-z]{2}$/.test(c)), "Use two-letter country codes separated by commas."),
    remote_mode: z.enum(["REMOTE_ONLY", "HYBRID_OK", "ONSITE_OK"]),
    employment_types: z.array(z.string()),
    seniority_levels: z.array(z.string()),
    min_salary: z.string().refine((v) => v === "" || Number(v) >= 0, "Enter a positive amount."),
    salary_currency: z.string().refine((v) => v === "" || /^[A-Za-z]{3}$/.test(v), "Three-letter code, e.g. USD."),
    salary_period: z.enum(["", "YEAR", "MONTH", "HOUR"]),
    needs_sponsorship: z.enum(["", "yes", "no"]),
    willing_to_relocate: z.enum(["", "yes", "no"]),
    notice_period_days: z.string().refine((v) => v === "" || (Number(v) >= 0 && Number(v) <= 365), "0–365 days."),
  })
  .refine((v) => [v.min_salary, v.salary_currency, v.salary_period].every((x) => x === "") || [v.min_salary, v.salary_currency, v.salary_period].every((x) => x !== ""), {
    path: ["min_salary"],
    message: "Provide amount, currency and period together, or leave all empty.",
  });
type Values = z.infer<typeof schema>;

const tri = (b: boolean | null) => (b === null ? "" : b ? "yes" : "no");
const fromTri = (v: string) => (v === "" ? null : v === "yes");

export function PreferencesForm({ prefs }: { prefs: Preferences }) {
  const save = useProfileMutation((p: Preferences) => profileApi.putPreferences(p));
  const { register, handleSubmit, formState } = useForm<Values>({
    resolver: zodResolver(schema),
    values: {
      target_roles: prefs.target_roles.join(", "),
      industries: prefs.industries.join(", "),
      preferred_locations: prefs.preferred_locations.join(", "),
      work_eligible_countries: prefs.work_eligible_countries.join(", "),
      remote_mode: prefs.remote_mode,
      employment_types: prefs.employment_types,
      seniority_levels: prefs.seniority_levels,
      min_salary: prefs.min_salary?.toString() ?? "",
      salary_currency: prefs.salary_currency ?? "",
      salary_period: prefs.salary_period ?? "",
      needs_sponsorship: tri(prefs.needs_sponsorship),
      willing_to_relocate: tri(prefs.willing_to_relocate),
      notice_period_days: prefs.notice_period_days?.toString() ?? "",
    },
  });
  const err = (f: keyof Values) => formState.errors[f]?.message ?? apiFieldError(save.error, f);

  const submit = handleSubmit((v) =>
    save.mutate({
      target_roles: splitList(v.target_roles),
      industries: splitList(v.industries),
      preferred_locations: splitList(v.preferred_locations),
      work_eligible_countries: splitList(v.work_eligible_countries).map((c) => c.toUpperCase()),
      remote_mode: v.remote_mode,
      employment_types: v.employment_types,
      seniority_levels: v.seniority_levels,
      min_salary: v.min_salary === "" ? null : Number(v.min_salary),
      salary_currency: v.salary_currency === "" ? null : v.salary_currency.toUpperCase(),
      salary_period: v.salary_period === "" ? null : v.salary_period,
      needs_sponsorship: fromTri(v.needs_sponsorship),
      willing_to_relocate: fromTri(v.willing_to_relocate),
      notice_period_days: v.notice_period_days === "" ? null : Number(v.notice_period_days),
    }),
  );

  return (
    <Panel>
      <form noValidate className="flex flex-col gap-space-md p-space-md" onSubmit={submit}>
        <div>
          <p className="label-caps text-outline">Search constraints</p>
          <h2 className="font-serif text-headline-sm text-on-surface">Career preferences</h2>
          <p className="mt-1 text-body-sm text-muted">These drive discovery and the hard eligibility filters. Leave a field empty when it does not apply; it will be treated as unknown, never guessed.</p>
        </div>
        <FormError error={save.error} />
        <TextInput label="Target roles" placeholder="Backend Engineer, Platform Engineer" hint="Comma-separated." error={err("target_roles")} {...register("target_roles")} />
        <div className="grid gap-space-md sm:grid-cols-2">
          <Select label="Remote work" error={err("remote_mode")} {...register("remote_mode")}>
            <option value="REMOTE_ONLY">Remote only</option>
            <option value="HYBRID_OK">Remote or hybrid</option>
            <option value="ONSITE_OK">Remote, hybrid or on-site</option>
          </Select>
          <TextInput
            label="Countries where you can legally work"
            placeholder="KE, UG"
            hint="Two-letter codes. Required for honest eligibility checks."
            error={err("work_eligible_countries")}
            {...register("work_eligible_countries")}
          />
        </div>
        <div className="grid gap-space-md sm:grid-cols-2">
          <TextInput label="Preferred locations" placeholder="Nairobi, Remote EMEA" error={err("preferred_locations")} {...register("preferred_locations")} />
          <TextInput label="Industries" placeholder="Fintech, Health" error={err("industries")} {...register("industries")} />
        </div>
        <fieldset>
          <legend className="text-[12px] font-medium text-on-surface-variant">Employment types</legend>
          <div className="mt-1 flex flex-wrap gap-x-space-md gap-y-1">
            {EMPLOYMENT.map((t) => (
              <Checkbox key={t} label={label(t)} value={t} {...register("employment_types")} />
            ))}
          </div>
        </fieldset>
        <fieldset>
          <legend className="text-[12px] font-medium text-on-surface-variant">Seniority</legend>
          <div className="mt-1 flex flex-wrap gap-x-space-md gap-y-1">
            {SENIORITY.map((t) => (
              <Checkbox key={t} label={label(t)} value={t} {...register("seniority_levels")} />
            ))}
          </div>
        </fieldset>
        <div className="grid gap-space-md sm:grid-cols-3">
          <TextInput label="Minimum salary" inputMode="decimal" error={err("min_salary")} {...register("min_salary")} />
          <TextInput label="Currency" placeholder="USD" maxLength={3} error={err("salary_currency")} {...register("salary_currency")} />
          <Select label="Per" error={err("salary_period")} {...register("salary_period")}>
            <option value="">—</option>
            <option value="YEAR">Year</option>
            <option value="MONTH">Month</option>
            <option value="HOUR">Hour</option>
          </Select>
        </div>
        <div className="grid gap-space-md sm:grid-cols-3">
          <Select label="Need visa sponsorship?" {...register("needs_sponsorship")}>
            <option value="">Not specified</option>
            <option value="yes">Yes</option>
            <option value="no">No</option>
          </Select>
          <Select label="Willing to relocate?" {...register("willing_to_relocate")}>
            <option value="">Not specified</option>
            <option value="yes">Yes</option>
            <option value="no">No</option>
          </Select>
          <TextInput label="Notice period (days)" inputMode="numeric" error={err("notice_period_days")} {...register("notice_period_days")} />
        </div>
        <div className="flex items-center justify-end gap-space-sm">
          {save.isSuccess && !formState.isDirty && (
            <span role="status" className="text-body-sm text-olive">
              Saved
            </span>
          )}
          <Button type="submit" disabled={save.isPending}>
            {save.isPending ? "Saving…" : "Save preferences"}
          </Button>
        </div>
      </form>
    </Panel>
  );
}
