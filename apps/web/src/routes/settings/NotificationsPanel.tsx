import { useState } from "react";
import { Button } from "../../components/ui/Button";
import { TextInput } from "../../components/ui/Form";
import { Panel } from "../../components/ui/Panel";
import { apiFieldError } from "../../lib/api";
import { useNotificationPrefs, useSaveNotificationPrefs, type NotificationPrefs } from "../../lib/notifications";

const toggles: [keyof NotificationPrefs, string][] = [
  ["high_match", "Strong new matches (at most five a day; the rest are grouped into one digest)"],
  ["approvals", "Approvals waiting for me"],
  ["workflow_events", "Workflows that need input or have finished, and skipped scheduled runs"],
  ["replies", "Recruiter replies and bounced emails"],
  ["follow_ups", "Follow-up drafts ready for review"],
  ["weekly_report", "Weekly career report (Mondays, only after a week with activity)"],
];

export function NotificationsPanel() {
  const p = useNotificationPrefs();
  return (
    <Panel>
      <div className="flex flex-col gap-space-sm p-space-md">
        <h2 className="font-serif text-headline-sm text-on-surface">Notifications</h2>
        <p className="text-body-sm text-muted">In-app only. CareerPilot does not email or text you notifications.</p>
        {p.isError && <p className="text-body-sm text-terracotta">{p.error.message}</p>}
        {p.data && <PrefsForm p={p.data} />}
      </div>
    </Panel>
  );
}

function PrefsForm({ p }: { p: NotificationPrefs }) {
  const save = useSaveNotificationPrefs();
  const [v, setV] = useState(p);
  return (
    <div className="flex flex-col gap-space-sm">
      {toggles.map(([k, label]) => (
        <label key={k} className="flex items-start gap-space-xs text-body-md text-on-surface">
          <input type="checkbox" checked={v[k] as boolean} onChange={(e) => setV({ ...v, [k]: e.target.checked })} className="mt-1" />
          {label}
        </label>
      ))}
      {v.high_match && (
        <TextInput
          label="Minimum match score for a notification"
          type="number"
          min={50}
          max={100}
          value={v.min_match_score}
          onChange={(e) => setV({ ...v, min_match_score: Number(e.target.value) })}
          error={apiFieldError(save.error, "min_match_score")}
        />
      )}
      {save.isSuccess && <p role="status" className="text-body-sm text-olive">Saved.</p>}
      <Button className="self-start" disabled={save.isPending} onClick={() => save.mutate(v)}>
        Save notification preferences
      </Button>
    </div>
  );
}
