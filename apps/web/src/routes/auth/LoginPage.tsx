import { zodResolver } from "@hookform/resolvers/zod";
import { useForm } from "react-hook-form";
import { Link, Navigate, useNavigate, useSearchParams } from "react-router";
import { z } from "zod";
import { Button } from "../../components/ui/Button";
import { FormError, TextInput } from "../../components/ui/Form";
import { safeNext } from "../../lib/redirect";
import { useLogin, useSession } from "../../lib/session";
import { AuthLayout } from "./AuthLayout";

const schema = z.object({
  email: z.email("Enter a valid email address."),
  password: z.string().min(1, "Enter your password."),
});
type Values = z.infer<typeof schema>;

export function LoginPage() {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const session = useSession();
  const login = useLogin();
  const { register, handleSubmit, formState } = useForm<Values>({ resolver: zodResolver(schema) });
  const next = safeNext(params.get("next"));

  if (session.data) return <Navigate to={next} replace />;

  return (
    <AuthLayout
      eyebrow="Sign in"
      title="Welcome back."
      footer={
        <>
          New to CareerPilot?{" "}
          <Link to="/register" className="font-semibold text-on-surface underline underline-offset-4">
            Create an account
          </Link>
        </>
      }
    >
      <form
        noValidate
        className="flex flex-col gap-space-md"
        onSubmit={handleSubmit((v) => login.mutate(v, { onSuccess: () => navigate(next, { replace: true }) }))}
      >
        {params.get("deleted") === "1" && (
          <p role="status" className="text-body-sm text-on-surface-variant">
            Your account and its data have been deleted.
          </p>
        )}
        <FormError error={login.error} />
        <TextInput label="Email" type="email" autoComplete="email" error={formState.errors.email?.message} {...register("email")} />
        <TextInput
          label="Password"
          type="password"
          autoComplete="current-password"
          error={formState.errors.password?.message}
          {...register("password")}
        />
        <div className="flex items-center justify-between">
          <Link to="/forgot-password" className="text-body-sm text-on-surface-variant underline underline-offset-4">
            Forgot your password?
          </Link>
          <Button type="submit" disabled={login.isPending}>
            {login.isPending ? "Signing in…" : "Sign in"}
          </Button>
        </div>
      </form>
    </AuthLayout>
  );
}
