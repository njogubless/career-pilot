import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useNavigate } from "react-router";
import { Button } from "../../components/ui/Button";
import { FormError, TextInput } from "../../components/ui/Form";
import { Panel } from "../../components/ui/Panel";
import { api, apiFieldError, apiPostBlob } from "../../lib/api";

function ExportForm() {
  const [password, setPassword] = useState("");
  const m = useMutation({
    mutationFn: () => apiPostBlob("/api/v1/users/me/export", { password }),
    onSuccess: (blob) => {
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `careerpilot-export-${new Date().toISOString().slice(0, 10)}.zip`;
      a.click();
      URL.revokeObjectURL(url);
      setPassword("");
    },
  });
  return (
    <form
      noValidate
      className="flex flex-col gap-space-sm"
      onSubmit={(e) => {
        e.preventDefault();
        m.mutate();
      }}
    >
      <h3 className="text-title-md text-on-surface">Download your data</h3>
      <p className="text-body-sm text-muted">
        A zip file with every record CareerPilot stores about you (profile, documents, matches, applications, outreach, workflows, notifications,
        your security log) and the files you uploaded. Passwords and access tokens are never included.
      </p>
      {!apiFieldError(m.error, "password") && <FormError error={m.error} />}
      <TextInput
        label="Confirm your password"
        type="password"
        autoComplete="current-password"
        value={password}
        onChange={(e) => setPassword(e.target.value)}
        error={apiFieldError(m.error, "password")}
      />
      {m.isSuccess && (
        <p role="status" className="text-body-sm text-olive">
          Your export has downloaded.
        </p>
      )}
      <Button type="submit" icon="download" variant="secondary" className="self-start" disabled={!password || m.isPending}>
        {m.isPending ? "Preparing…" : "Download my data"}
      </Button>
    </form>
  );
}

function DeleteForm() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const m = useMutation({
    mutationFn: () => api.post<void>("/api/v1/users/me/delete", { password, confirm }),
    onSuccess: () => {
      qc.clear();
      navigate("/login?deleted=1", { replace: true });
    },
  });
  return (
    <form
      noValidate
      className="flex flex-col gap-space-sm"
      onSubmit={(e) => {
        e.preventDefault();
        m.mutate();
      }}
    >
      <h3 className="text-title-md text-on-surface">Delete your account</h3>
      <p className="text-body-sm text-muted">
        Permanently deletes your profile, documents, matches, applications, outreach history, workflows and schedules, and revokes Gmail access.
        This cannot be undone. Download your data first if you want a copy. A record that the account existed and was deleted is kept in the
        security log, without your IP addresses or any details.
      </p>
      {!apiFieldError(m.error, "password") && !apiFieldError(m.error, "confirm") && <FormError error={m.error} />}
      <TextInput
        label="Your password"
        type="password"
        autoComplete="current-password"
        value={password}
        onChange={(e) => setPassword(e.target.value)}
        error={apiFieldError(m.error, "password")}
      />
      <TextInput label="Type DELETE to confirm" value={confirm} onChange={(e) => setConfirm(e.target.value)} error={apiFieldError(m.error, "confirm")} />
      <Button type="submit" variant="destructive" className="self-start" disabled={!password || confirm !== "DELETE" || m.isPending}>
        {m.isPending ? "Deleting…" : "Delete my account permanently"}
      </Button>
    </form>
  );
}

/** Data rights: export and account deletion (ADR-027). */
export function DataPanel() {
  return (
    <Panel>
      <div className="flex flex-col gap-space-lg p-space-md">
        <h2 className="font-serif text-headline-sm text-on-surface">Your data</h2>
        <ExportForm />
        <div className="border-t border-stone pt-space-md">
          <DeleteForm />
        </div>
      </div>
    </Panel>
  );
}
