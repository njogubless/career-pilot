import { useState } from "react";
import { useSearchParams } from "react-router";
import { Button } from "../../components/ui/Button";
import { TextArea, TextInput } from "../../components/ui/Form";
import { Panel } from "../../components/ui/Panel";
import { Tag } from "../../components/ui/Tag";
import { apiFieldError } from "../../lib/api";
import { relativeTime } from "../../lib/format";
import { useGmailActions, useGmailStatus, useOutreachSettings, useSaveOutreachSettings, type OutreachSettings } from "../../lib/outreach";

/** Gmail connection: minimal scopes, tokens encrypted, revocable at any time. */
export function GmailPanel() {
  const status = useGmailStatus();
  const { connect, disconnect } = useGmailActions();
  const [params] = useSearchParams();
  const result = params.get("gmail");
  const g = status.data;
  return (
    <Panel>
      <div id="gmail" className="flex flex-col gap-space-sm p-space-md">
        <div className="flex items-center justify-between">
          <h2 className="font-serif text-headline-sm text-on-surface">Gmail</h2>
          {g?.account && <Tag tone={g.account.status === "CONNECTED" ? "olive" : "terracotta"}>{g.account.status === "CONNECTED" ? "Connected" : "Reconnect needed"}</Tag>}
        </div>
        {result === "connected" && <p role="status" className="text-body-sm text-olive">Gmail connected.</p>}
        {result === "error" && <p role="alert" className="text-body-sm text-terracotta">{params.get("message") ?? "The connection failed."}</p>}
        {g && !g.configured && (
          <p className="text-body-md text-on-surface-variant">
            Gmail is not configured on this server. An operator must add Google OAuth credentials before accounts can be connected.
          </p>
        )}
        {g?.configured && (
          <>
            <p className="text-body-sm text-on-surface-variant">
              CareerPilot asks for two permissions only: send email, and read message headers to detect replies. It cannot read your email
              content. Tokens are encrypted, and you can disconnect at any time; disconnecting also revokes the access at Google.
            </p>
            {g.account ? (
              <>
                <p className="font-mono text-label-md text-on-surface">
                  {g.account.email} · connected {relativeTime(g.account.connected_at)}
                </p>
                <div className="flex gap-space-sm">
                  {g.account.status !== "CONNECTED" && (
                    <Button disabled={connect.isPending} onClick={() => connect.mutate()}>
                      Reconnect
                    </Button>
                  )}
                  <Button variant="destructive" disabled={disconnect.isPending} onClick={() => disconnect.mutate()}>
                    Disconnect
                  </Button>
                </div>
              </>
            ) : (
              <Button icon="link" className="self-start" disabled={connect.isPending} onClick={() => connect.mutate()}>
                Connect Gmail
              </Button>
            )}
            {(connect.isError || disconnect.isError) && <p className="text-body-sm text-terracotta">{(connect.error ?? disconnect.error)?.message}</p>}
          </>
        )}
      </div>
    </Panel>
  );
}

function PrefsForm({ s }: { s: OutreachSettings }) {
  const save = useSaveOutreachSettings();
  const [v, setV] = useState(s);
  const num = (k: keyof OutreachSettings) => (e: { target: { value: string } }) => setV({ ...v, [k]: Number(e.target.value) });
  return (
    <div className="flex flex-col gap-space-sm">
      <TextInput label="Emails per 24 hours" type="number" min={1} max={50} value={v.daily_send_limit} onChange={num("daily_send_limit")} error={apiFieldError(save.error, "daily_send_limit")} />
      <TextInput label="Days between emails to the same person" type="number" min={1} max={60} value={v.recipient_cooldown_days} onChange={num("recipient_cooldown_days")} error={apiFieldError(save.error, "recipient_cooldown_days")} />
      <label className="flex items-start gap-space-xs text-body-md text-on-surface">
        <input type="checkbox" checked={v.follow_ups_enabled} onChange={(e) => setV({ ...v, follow_ups_enabled: e.target.checked })} className="mt-1" />
        Prepare follow-up drafts when there's no reply. They are never sent without your approval, and a reply cancels them.
      </label>
      {v.follow_ups_enabled && (
        <div className="grid grid-cols-2 gap-space-sm">
          <TextInput label="Follow up after (days)" type="number" min={2} max={30} value={v.follow_up_after_days} onChange={num("follow_up_after_days")} error={apiFieldError(save.error, "follow_up_after_days")} />
          <TextInput label="Maximum follow-ups" type="number" min={0} max={2} value={v.max_follow_ups} onChange={num("max_follow_ups")} error={apiFieldError(save.error, "max_follow_ups")} />
        </div>
      )}
      <TextArea label="Signature (optional)" rows={2} maxLength={500} value={v.signature} onChange={(e) => setV({ ...v, signature: e.target.value })} />
      {save.isSuccess && <p role="status" className="text-body-sm text-olive">Saved.</p>}
      <Button className="self-start" disabled={save.isPending} onClick={() => save.mutate(v)}>
        Save outreach preferences
      </Button>
    </div>
  );
}

export function OutreachPrefsPanel() {
  const s = useOutreachSettings();
  return (
    <Panel>
      <div className="flex flex-col gap-space-sm p-space-md">
        <h2 className="font-serif text-headline-sm text-on-surface">Outreach preferences</h2>
        {s.data && <PrefsForm s={s.data} />}
      </div>
    </Panel>
  );
}
