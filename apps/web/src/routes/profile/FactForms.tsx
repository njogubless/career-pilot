import { zodResolver } from "@hookform/resolvers/zod";
import { apiFieldError } from "../../lib/api";
import { useForm, useWatch } from "react-hook-form";
import { z } from "zod";
import { Button } from "../../components/ui/Button";
import { Checkbox, FormError, TextArea, TextInput } from "../../components/ui/Form";
import { Modal } from "../../components/ui/Modal";
import type { Certification, Education, Experience, FactKind, Project } from "../../lib/profile";
import { profileApi, useProfileMutation } from "../../lib/profile";
import { fromMonth, toMonth } from "../../lib/redirect";

const optionalUrl = z
  .string()
  .trim()
  .refine((v) => v === "" || /^https?:\/\/\S+$/i.test(v), "Enter a full http(s) URL.");

function useSave(kind: FactKind, id: string | undefined) {
  return useProfileMutation((body: object) => (id ? profileApi.update(kind, id, body) : profileApi.create(kind, body)));
}

type FormShellProps = { title: string; onClose: () => void; pending: boolean; error: unknown; onSubmit: () => void; children: React.ReactNode };

function FormShell({ title, onClose, pending, error, onSubmit, children }: FormShellProps) {
  return (
    <Modal
      title={title}
      onClose={onClose}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="fact-form" disabled={pending}>
            {pending ? "Saving…" : "Save"}
          </Button>
        </>
      }
    >
      <form
        id="fact-form"
        noValidate
        className="flex flex-col gap-space-md"
        onSubmit={(e) => {
          e.preventDefault();
          onSubmit();
        }}
      >
        <FormError error={error} />
        {children}
      </form>
    </Modal>
  );
}

// ---- Experience ----

const experienceSchema = z
  .object({
    employer: z.string().trim().min(1, "Required.").max(200),
    title: z.string().trim().min(1, "Required.").max(200),
    location: z.string().trim().max(200),
    start: z.string(),
    end: z.string(),
    is_current: z.boolean(),
    description: z.string().max(5000),
    achievements: z.string().max(20000),
  })
  .refine((v) => v.is_current || !v.start || !v.end || v.end >= v.start, { path: ["end"], message: "End must be after start." });
type ExperienceValues = z.infer<typeof experienceSchema>;

export function ExperienceForm({ item, onClose }: { item?: Experience; onClose: () => void }) {
  const save = useSave("experiences", item?.id);
  const { register, handleSubmit, control, formState } = useForm<ExperienceValues>({
    resolver: zodResolver(experienceSchema),
    defaultValues: {
      employer: item?.employer ?? "",
      title: item?.title ?? "",
      location: item?.location ?? "",
      start: toMonth(item?.start_date),
      end: toMonth(item?.end_date),
      is_current: item?.is_current ?? false,
      description: item?.description ?? "",
      achievements: (item?.achievements ?? []).join("\n"),
    },
  });
  const current = useWatch({ control, name: "is_current" });
  const e = (f: keyof ExperienceValues, api?: string) => formState.errors[f]?.message ?? apiFieldError(save.error, api ?? f);
  const submit = handleSubmit((v) =>
    save.mutate(
      {
        employer: v.employer,
        title: v.title,
        location: v.location,
        start_date: fromMonth(v.start),
        end_date: v.is_current ? null : fromMonth(v.end),
        is_current: v.is_current,
        description: v.description,
        achievements: v.achievements.split("\n").map((s) => s.trim()).filter(Boolean),
      },
      { onSuccess: onClose },
    ),
  );

  return (
    <FormShell title={item ? "Edit experience" : "Add experience"} onClose={onClose} pending={save.isPending} error={save.error} onSubmit={submit}>
      <div className="grid gap-space-md sm:grid-cols-2">
        <TextInput label="Job title" error={e("title")} {...register("title")} />
        <TextInput label="Employer" error={e("employer")} {...register("employer")} />
      </div>
      <TextInput label="Location" error={e("location")} {...register("location")} />
      <div className="grid gap-space-md sm:grid-cols-2">
        <TextInput label="Start" type="month" error={e("start", "start_date")} {...register("start")} />
        <TextInput label="End" type="month" disabled={current} error={e("end", "end_date")} {...register("end")} />
      </div>
      <Checkbox label="I currently work here" {...register("is_current")} />
      <TextArea label="Description" error={e("description")} {...register("description")} />
      <TextArea label="Achievements" hint="One per line. Keep them factual; they are used as evidence in tailored applications." error={e("achievements")} {...register("achievements")} />
    </FormShell>
  );
}

// ---- Education ----

const educationSchema = z.object({
  institution: z.string().trim().min(1, "Required.").max(200),
  degree: z.string().trim().max(200),
  field: z.string().trim().max(200),
  start: z.string(),
  end: z.string(),
});
type EducationValues = z.infer<typeof educationSchema>;

export function EducationForm({ item, onClose }: { item?: Education; onClose: () => void }) {
  const save = useSave("education", item?.id);
  const { register, handleSubmit, formState } = useForm<EducationValues>({
    resolver: zodResolver(educationSchema),
    defaultValues: {
      institution: item?.institution ?? "",
      degree: item?.degree ?? "",
      field: item?.field ?? "",
      start: toMonth(item?.start_date),
      end: toMonth(item?.end_date),
    },
  });
  const e = (f: keyof EducationValues, api?: string) => formState.errors[f]?.message ?? apiFieldError(save.error, api ?? f);
  const submit = handleSubmit((v) =>
    save.mutate(
      { institution: v.institution, degree: v.degree, field: v.field, start_date: fromMonth(v.start), end_date: fromMonth(v.end) },
      { onSuccess: onClose },
    ),
  );
  return (
    <FormShell title={item ? "Edit education" : "Add education"} onClose={onClose} pending={save.isPending} error={save.error} onSubmit={submit}>
      <TextInput label="Institution" error={e("institution")} {...register("institution")} />
      <div className="grid gap-space-md sm:grid-cols-2">
        <TextInput label="Degree" error={e("degree")} {...register("degree")} />
        <TextInput label="Field of study" error={e("field")} {...register("field")} />
      </div>
      <div className="grid gap-space-md sm:grid-cols-2">
        <TextInput label="Start" type="month" error={e("start", "start_date")} {...register("start")} />
        <TextInput label="End" type="month" error={e("end", "end_date")} {...register("end")} />
      </div>
    </FormShell>
  );
}

// ---- Certification ----

const certSchema = z.object({
  name: z.string().trim().min(1, "Required.").max(200),
  issuer: z.string().trim().max(200),
  issued: z.string(),
  expires: z.string(),
  credential_url: optionalUrl,
});
type CertValues = z.infer<typeof certSchema>;

export function CertificationForm({ item, onClose }: { item?: Certification; onClose: () => void }) {
  const save = useSave("certifications", item?.id);
  const { register, handleSubmit, formState } = useForm<CertValues>({
    resolver: zodResolver(certSchema),
    defaultValues: {
      name: item?.name ?? "",
      issuer: item?.issuer ?? "",
      issued: toMonth(item?.issued_date),
      expires: toMonth(item?.expires_date),
      credential_url: item?.credential_url ?? "",
    },
  });
  const e = (f: keyof CertValues, api?: string) => formState.errors[f]?.message ?? apiFieldError(save.error, api ?? f);
  const submit = handleSubmit((v) =>
    save.mutate(
      { name: v.name, issuer: v.issuer, issued_date: fromMonth(v.issued), expires_date: fromMonth(v.expires), credential_url: v.credential_url },
      { onSuccess: onClose },
    ),
  );
  return (
    <FormShell title={item ? "Edit certification" : "Add certification"} onClose={onClose} pending={save.isPending} error={save.error} onSubmit={submit}>
      <TextInput label="Certification" error={e("name")} {...register("name")} />
      <TextInput label="Issuer" error={e("issuer")} {...register("issuer")} />
      <div className="grid gap-space-md sm:grid-cols-2">
        <TextInput label="Issued" type="month" error={e("issued", "issued_date")} {...register("issued")} />
        <TextInput label="Expires" type="month" error={e("expires", "expires_date")} {...register("expires")} />
      </div>
      <TextInput label="Credential URL" type="url" error={e("credential_url")} {...register("credential_url")} />
    </FormShell>
  );
}

// ---- Project ----

const projectSchema = z.object({
  name: z.string().trim().min(1, "Required.").max(200),
  description: z.string().max(5000),
  url: optionalUrl,
});
type ProjectValues = z.infer<typeof projectSchema>;

export function ProjectForm({ item, onClose }: { item?: Project; onClose: () => void }) {
  const save = useSave("projects", item?.id);
  const { register, handleSubmit, formState } = useForm<ProjectValues>({
    resolver: zodResolver(projectSchema),
    defaultValues: { name: item?.name ?? "", description: item?.description ?? "", url: item?.url ?? "" },
  });
  const e = (f: keyof ProjectValues) => formState.errors[f]?.message ?? apiFieldError(save.error, f);
  const submit = handleSubmit((v) => save.mutate(v, { onSuccess: onClose }));
  return (
    <FormShell title={item ? "Edit project" : "Add project"} onClose={onClose} pending={save.isPending} error={save.error} onSubmit={submit}>
      <TextInput label="Project name" error={e("name")} {...register("name")} />
      <TextArea label="Description" error={e("description")} {...register("description")} />
      <TextInput label="URL" type="url" error={e("url")} {...register("url")} />
    </FormShell>
  );
}
