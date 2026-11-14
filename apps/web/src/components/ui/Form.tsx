import clsx from "clsx";
import { forwardRef, useId, type InputHTMLAttributes, type ReactNode, type SelectHTMLAttributes, type TextareaHTMLAttributes } from "react";
import { ApiError } from "../../lib/api";

const inputBase =
  "w-full rounded-sm border bg-surface-container-lowest px-3 py-2 text-body-md text-on-surface placeholder:text-outline " +
  "focus:border-graphite focus:outline-none focus-visible:outline-none disabled:opacity-60";

type FieldProps = { label: string; error?: string; hint?: string; children: (id: string, describedBy?: string) => ReactNode };

/** Label above, helper/error below (DESIGN.md: helper labels placed strictly above the field). */
export function Field({ label, error, hint, children }: FieldProps) {
  const id = useId();
  const hintId = hint ? `${id}-hint` : undefined;
  const errId = error ? `${id}-err` : undefined;
  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={id} className="text-[12px] font-medium text-on-surface-variant">
        {label}
      </label>
      {children(id, [hintId, errId].filter(Boolean).join(" ") || undefined)}
      {hint && !error && (
        <p id={hintId} className="text-body-sm text-muted">
          {hint}
        </p>
      )}
      {error && (
        <p id={errId} className="text-body-sm text-terracotta" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}

type InputProps = InputHTMLAttributes<HTMLInputElement> & { label: string; error?: string; hint?: string };

export const TextInput = forwardRef<HTMLInputElement, InputProps>(function TextInput({ label, error, hint, className, ...rest }, ref) {
  return (
    <Field label={label} error={error} hint={hint}>
      {(id, describedBy) => (
        <input
          ref={ref}
          id={id}
          aria-invalid={error ? true : undefined}
          aria-describedby={describedBy}
          className={clsx(inputBase, error ? "border-terracotta" : "border-stone", className)}
          {...rest}
        />
      )}
    </Field>
  );
});

type AreaProps = TextareaHTMLAttributes<HTMLTextAreaElement> & { label: string; error?: string; hint?: string };

export const TextArea = forwardRef<HTMLTextAreaElement, AreaProps>(function TextArea({ label, error, hint, className, ...rest }, ref) {
  return (
    <Field label={label} error={error} hint={hint}>
      {(id, describedBy) => (
        <textarea
          ref={ref}
          id={id}
          aria-invalid={error ? true : undefined}
          aria-describedby={describedBy}
          className={clsx(inputBase, "min-h-24", error ? "border-terracotta" : "border-stone", className)}
          {...rest}
        />
      )}
    </Field>
  );
});

type SelectProps = SelectHTMLAttributes<HTMLSelectElement> & { label: string; error?: string; hint?: string; children: ReactNode };

export const Select = forwardRef<HTMLSelectElement, SelectProps>(function Select({ label, error, hint, className, children, ...rest }, ref) {
  return (
    <Field label={label} error={error} hint={hint}>
      {(id, describedBy) => (
        <select
          ref={ref}
          id={id}
          aria-invalid={error ? true : undefined}
          aria-describedby={describedBy}
          className={clsx(inputBase, error ? "border-terracotta" : "border-stone", className)}
          {...rest}
        >
          {children}
        </select>
      )}
    </Field>
  );
});

export const Checkbox = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement> & { label: ReactNode }>(function Checkbox(
  { label, className, ...rest },
  ref,
) {
  return (
    <label className={clsx("inline-flex cursor-pointer items-center gap-2 text-body-md text-on-surface", className)}>
      <input ref={ref} type="checkbox" className="h-4 w-4 rounded-xs accent-graphite" {...rest} />
      <span>{label}</span>
    </label>
  );
});

/** Form-level error banner. Field-level details are shown next to fields. */
export function FormError({ error }: { error: unknown }) {
  if (!error) return null;
  const message = error instanceof ApiError ? error.message : "Something went wrong. Please try again.";
  return (
    <div role="alert" className="rounded-sm border border-terracotta bg-tertiary-fixed/40 px-3 py-2 text-body-sm text-on-tertiary-fixed-variant">
      {message}
    </div>
  );
}
