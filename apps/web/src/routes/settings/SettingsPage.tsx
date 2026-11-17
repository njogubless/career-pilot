import { zodResolver } from "@hookform/resolvers/zod";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useForm } from "react-hook-form";
import { useNavigate } from "react-router";
import { z } from "zod";
import { Button } from "../../components/ui/Button";
import { FormError, TextInput } from "../../components/ui/Form";
import { Panel } from "../../components/ui/Panel";
import { Tag } from "../../components/ui/Tag";
import { api, apiFieldError } from "../../lib/api";
import { AutomationPanel } from "./AutomationPanel";
import { GmailPanel, OutreachPrefsPanel } from "./GmailPanel";
import { sessionKey, useLogout, useSession, type User } from "../../lib/session";

function AccountPanel({ user }: { user: User }) {
  const qc = useQueryClient();
  const schema = z.object({ display_name: z.string().trim().min(1, "Required.").max(120) });
  const { register, handleSubmit, formState } = useForm<{ display_name: string }>({
    resolver: zodResolver(schema),
    values: { display_name: user.display_name },
  });
  const m = useMutation({
    mutationFn: (v: { display_name: string }) => api.patch<User>("/api/v1/users/me", v),
    onSuccess: (u) => qc.setQueryData(sessionKey, u),
  });
  return (
    <Panel>
      <form noValidate className="flex flex-col gap-space-md p-space-md" onSubmit={handleSubmit((v) => m.mutate(v))}>
        <h2 className="font-serif text-headline-sm text-on-surface">Account</h2>
        <FormError error={m.error} />
        <TextInput label="Name" error={formState.errors.display_name?.message ?? apiFieldError(m.error, "display_name")} {...register("display_name")} />
        <TextInput label="Email" value={user.email} disabled readOnly hint="Email changes are not supported yet." />
        <div className="flex justify-end">
          <Button type="submit" disabled={m.isPending}>
            Save
          </Button>
        </div>
      </form>
    </Panel>
  );
}

function SecurityPanel() {
  const navigate = useNavigate();
  const logoutAll = useLogout(true);
  const schema = z
    .object({ current_password: z.string().min(1, "Required."), new_password: z.string().min(10, "Use at least 10 characters."), confirm: z.string() })
    .refine((v) => v.new_password === v.confirm, { path: ["confirm"], message: "Passwords do not match." });
  type V = z.infer<typeof schema>;
  const { register, handleSubmit, formState, reset } = useForm<V>({ resolver: zodResolver(schema) });
  const m = useMutation({
    mutationFn: (v: V) => api.post<void>("/api/v1/auth/password/change", { current_password: v.current_password, new_password: v.new_password }),
    onSuccess: () => reset(),
  });
  return (
    <Panel>
      <form noValidate className="flex flex-col gap-space-md p-space-md" onSubmit={handleSubmit((v) => m.mutate(v))}>
        <h2 className="font-serif text-headline-sm text-on-surface">Security</h2>
        <FormError error={m.error} />
        {m.isSuccess && (
          <p role="status" className="text-body-sm text-olive">
            Password changed. Your other sessions were signed out.
          </p>
        )}
        <TextInput
          label="Current password"
          type="password"
          autoComplete="current-password"
          error={formState.errors.current_password?.message ?? apiFieldError(m.error, "current_password")}
          {...register("current_password")}
        />
        <TextInput
          label="New password"
          type="password"
          autoComplete="new-password"
          error={formState.errors.new_password?.message ?? apiFieldError(m.error, "new_password")}
          {...register("new_password")}
        />
        <TextInput label="Confirm new password" type="password" autoComplete="new-password" error={formState.errors.confirm?.message} {...register("confirm")} />
        <div className="flex flex-wrap justify-between gap-space-sm">
          <Button
            variant="destructive"
            disabled={logoutAll.isPending}
            onClick={() => {
              if (window.confirm("Sign out of every device, including this one?")) logoutAll.mutate(undefined, { onSettled: () => navigate("/login") });
            }}
          >
            Sign out everywhere
          </Button>
          <Button type="submit" disabled={m.isPending}>
            Change password
          </Button>
        </div>
      </form>
    </Panel>
  );
}

const upcoming = [
  ["Per-action automation permissions", "Fine-grained agent permissions arrive with the Automation Centre (Phase 8). Mode, daily limit and the pause switch are above."],
  ["Data export & account deletion", "Planned for Phase 10. Until then, contact support to export or delete your data."],
] as const;

export function SettingsPage() {
  const session = useSession();
  if (!session.data) return null;
  return (
    <div className="flex flex-col gap-space-lg">
      <header className="flex flex-col gap-space-xs">
        <Tag tone="graphite" className="self-start">
          Dossier & systems
        </Tag>
        <h1 className="font-serif text-headline-lg-mobile text-on-surface md:text-headline-lg">Settings</h1>
      </header>
      <div className="grid grid-cols-1 items-start gap-space-lg xl:grid-cols-2">
        <AccountPanel user={session.data} />
        <SecurityPanel />
        <AutomationPanel />
        <GmailPanel />
        <OutreachPrefsPanel />
      </div>
      <Panel>
        <h2 className="p-space-md font-serif text-headline-sm text-on-surface">Not available yet</h2>
        <ul className="divide-y divide-stone border-t border-stone">
          {upcoming.map(([title, text]) => (
            <li key={title} className="p-space-md">
              <p className="text-title-md text-on-surface">{title}</p>
              <p className="text-body-sm text-muted">{text}</p>
            </li>
          ))}
        </ul>
      </Panel>
    </div>
  );
}
