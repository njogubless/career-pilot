import { zodResolver } from "@hookform/resolvers/zod";
import { apiFieldError } from "../../lib/api";
import { useFieldArray, useForm } from "react-hook-form";
import { z } from "zod";
import { Button } from "../../components/ui/Button";
import { FormError, TextArea, TextInput } from "../../components/ui/Form";
import { Panel } from "../../components/ui/Panel";
import type { Basics } from "../../lib/profile";
import { profileApi, useProfileMutation } from "../../lib/profile";

const schema = z.object({
  headline: z.string().trim().max(200),
  summary: z.string().trim().max(5000),
  location_city: z.string().trim().max(120),
  location_country: z
    .string()
    .trim()
    .refine((v) => v === "" || /^[A-Za-z]{2}$/.test(v), "Use a two-letter country code, e.g. KE."),
  links: z
    .array(
      z.object({
        label: z.string().trim().min(1, "Required.").max(60),
        url: z.string().trim().regex(/^https?:\/\/\S+$/i, "Enter a full http(s) URL."),
      }),
    )
    .max(20),
});
type Values = z.infer<typeof schema>;

export function BasicsForm({ basics }: { basics: Basics }) {
  const save = useProfileMutation((b: Basics) => profileApi.putBasics(b));
  const { register, handleSubmit, control, formState } = useForm<Values>({
    resolver: zodResolver(schema),
    values: {
      headline: basics.headline,
      summary: basics.summary,
      location_city: basics.location_city,
      location_country: basics.location_country ?? "",
      links: basics.links,
    },
  });
  const links = useFieldArray({ control, name: "links" });
  const e = (f: string) => apiFieldError(save.error, f);

  return (
    <Panel>
      <form
        noValidate
        className="flex flex-col gap-space-md p-space-md"
        onSubmit={handleSubmit((v) =>
          save.mutate({ ...v, location_country: v.location_country ? v.location_country.toUpperCase() : null, years_experience: basics.years_experience }),
        )}
      >
        <div>
          <p className="label-caps text-outline">Dossier</p>
          <h2 className="font-serif text-headline-sm text-on-surface">Personal details</h2>
        </div>
        <FormError error={save.error} />
        <TextInput label="Professional headline" placeholder="e.g. Backend Engineer" error={formState.errors.headline?.message ?? e("headline")} {...register("headline")} />
        <TextArea label="Summary" error={formState.errors.summary?.message ?? e("summary")} {...register("summary")} />
        <div className="grid gap-space-md sm:grid-cols-2">
          <TextInput label="City" error={formState.errors.location_city?.message ?? e("location_city")} {...register("location_city")} />
          <TextInput
            label="Country of residence"
            placeholder="KE"
            maxLength={2}
            hint="Two-letter ISO code."
            error={formState.errors.location_country?.message ?? e("location_country")}
            {...register("location_country")}
          />
        </div>
        <fieldset className="flex flex-col gap-space-sm">
          <legend className="text-[12px] font-medium text-on-surface-variant">Links (portfolio, GitHub, LinkedIn)</legend>
          {links.fields.map((f, i) => (
            <div key={f.id} className="grid grid-cols-[1fr_2fr_auto] items-end gap-space-sm">
              <TextInput label="Label" error={formState.errors.links?.[i]?.label?.message} {...register(`links.${i}.label`)} />
              <TextInput label="URL" type="url" error={formState.errors.links?.[i]?.url?.message ?? e(`links.${i}.url`)} {...register(`links.${i}.url`)} />
              <Button variant="ghost" icon="close" aria-label={`Remove link ${i + 1}`} onClick={() => links.remove(i)}>
                <span className="sr-only">Remove</span>
              </Button>
            </div>
          ))}
          <div>
            <Button variant="secondary" icon="add_link" onClick={() => links.append({ label: "", url: "" })}>
              Add link
            </Button>
          </div>
        </fieldset>
        <div className="flex items-center justify-end gap-space-sm">
          {save.isSuccess && !formState.isDirty && (
            <span role="status" className="text-body-sm text-olive">
              Saved
            </span>
          )}
          <Button type="submit" disabled={save.isPending}>
            {save.isPending ? "Saving…" : "Save details"}
          </Button>
        </div>
      </form>
    </Panel>
  );
}
