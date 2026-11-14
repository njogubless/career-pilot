import { zodResolver } from "@hookform/resolvers/zod";
import { useMutation } from "@tanstack/react-query";
import { useMemo } from "react";
import { useForm } from "react-hook-form";
import { Link, useLocation } from "react-router";
import { z } from "zod";
import { Button } from "../../components/ui/Button";
import { FormError, TextInput } from "../../components/ui/Form";
import { api, apiFieldError } from "../../lib/api";
import { AuthLayout } from "./AuthLayout";

const backToLogin = (
  <Link to="/login" className="font-semibold text-on-surface underline underline-offset-4">
    Back to sign in
  </Link>
);

export function ForgotPasswordPage() {
  const schema = z.object({ email: z.email("Enter a valid email address.") });
  const { register, handleSubmit, formState } = useForm<{ email: string }>({ resolver: zodResolver(schema) });
  const m = useMutation({ mutationFn: (v: { email: string }) => api.post<{ message: string }>("/api/v1/auth/password/forgot", v) });

  return (
    <AuthLayout eyebrow="Password recovery" title="Reset your password." footer={backToLogin}>
      {m.isSuccess ? (
        <p role="status" className="text-body-md text-on-surface">
          {m.data.message} The link expires in 30 minutes.
        </p>
      ) : (
        <form noValidate className="flex flex-col gap-space-md" onSubmit={handleSubmit((v) => m.mutate(v))}>
          <FormError error={m.error} />
          <TextInput label="Email" type="email" autoComplete="email" error={formState.errors.email?.message} {...register("email")} />
          <Button type="submit" disabled={m.isPending}>
            {m.isPending ? "Sending…" : "Send reset link"}
          </Button>
        </form>
      )}
    </AuthLayout>
  );
}

/** Reads the token from the URL fragment, which browsers never send to servers. */
export function ResetPasswordPage() {
  const { hash } = useLocation();
  const token = useMemo(() => new URLSearchParams(hash.replace(/^#/, "")).get("token") ?? "", [hash]);
  const schema = z
    .object({ password: z.string().min(10, "Use at least 10 characters.").max(256), confirm: z.string() })
    .refine((v) => v.password === v.confirm, { path: ["confirm"], message: "Passwords do not match." });
  type V = z.infer<typeof schema>;
  const { register, handleSubmit, formState } = useForm<V>({ resolver: zodResolver(schema) });
  const m = useMutation({ mutationFn: (v: V) => api.post<void>("/api/v1/auth/password/reset", { token, password: v.password }) });

  return (
    <AuthLayout eyebrow="Password recovery" title="Choose a new password." footer={backToLogin}>
      {!token ? (
        <p role="alert" className="text-body-md text-terracotta">
          This reset link is incomplete. Request a new one from the sign-in page.
        </p>
      ) : m.isSuccess ? (
        <p role="status" className="text-body-md text-on-surface">
          Your password has been changed and other sessions were signed out. You can now sign in.
        </p>
      ) : (
        <form noValidate className="flex flex-col gap-space-md" onSubmit={handleSubmit((v) => m.mutate(v))}>
          <FormError error={m.error} />
          <TextInput
            label="New password"
            type="password"
            autoComplete="new-password"
            error={formState.errors.password?.message ?? apiFieldError(m.error, "password")}
            {...register("password")}
          />
          <TextInput
            label="Confirm new password"
            type="password"
            autoComplete="new-password"
            error={formState.errors.confirm?.message}
            {...register("confirm")}
          />
          <Button type="submit" disabled={m.isPending}>
            {m.isPending ? "Saving…" : "Set new password"}
          </Button>
        </form>
      )}
    </AuthLayout>
  );
}
