import { zodResolver } from "@hookform/resolvers/zod";
import { apiFieldError } from "../../lib/api";
import { useForm } from "react-hook-form";
import { Link, Navigate, useNavigate } from "react-router";
import { z } from "zod";
import { Button } from "../../components/ui/Button";
import { FormError, TextInput } from "../../components/ui/Form";
import { useRegister, useSession } from "../../lib/session";
import { AuthLayout } from "./AuthLayout";

const schema = z.object({
  display_name: z.string().trim().min(1, "Enter your name.").max(120),
  email: z.email("Enter a valid email address."),
  password: z.string().min(10, "Use at least 10 characters.").max(256),
});
type Values = z.infer<typeof schema>;

export function RegisterPage() {
  const navigate = useNavigate();
  const session = useSession();
  const reg = useRegister();
  const { register, handleSubmit, formState } = useForm<Values>({ resolver: zodResolver(schema) });

  if (session.data) return <Navigate to="/" replace />;
  const err = (f: keyof Values) => formState.errors[f]?.message ?? apiFieldError(reg.error, f);

  return (
    <AuthLayout
      eyebrow="Create account"
      title="Start with your profile."
      footer={
        <>
          Already have an account?{" "}
          <Link to="/login" className="font-semibold text-on-surface underline underline-offset-4">
            Sign in
          </Link>
        </>
      }
    >
      <form
        noValidate
        className="flex flex-col gap-space-md"
        onSubmit={handleSubmit((v) => reg.mutate(v, { onSuccess: () => navigate("/profile?welcome=1", { replace: true }) }))}
      >
        <FormError error={reg.error} />
        <TextInput label="Full name" autoComplete="name" error={err("display_name")} {...register("display_name")} />
        <TextInput label="Email" type="email" autoComplete="email" error={err("email")} {...register("email")} />
        <TextInput
          label="Password"
          type="password"
          autoComplete="new-password"
          hint="At least 10 characters. A passphrase works well."
          error={err("password")}
          {...register("password")}
        />
        <p className="text-body-sm text-muted">
          Your CV and profile are private to your account. AI features send document text to the configured AI provider only when you
          ask CareerPilot to analyse it.
        </p>
        <Button type="submit" disabled={reg.isPending}>
          {reg.isPending ? "Creating account…" : "Create account"}
        </Button>
      </form>
    </AuthLayout>
  );
}
