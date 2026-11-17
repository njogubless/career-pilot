import { useState } from "react";
import { Button } from "../../components/ui/Button";
import { TextInput } from "../../components/ui/Form";
import { Panel } from "../../components/ui/Panel";
import { Tag } from "../../components/ui/Tag";
import { apiFieldError } from "../../lib/api";
import { useAutomation, useAutomationActions, type AutomationSettings } from "../../lib/applications";
import { relativeTime } from "../../lib/format";

const modes: { value: AutomationSettings["mode"]; label: string; detail: string }[] = [
  { value: "DISCOVERY_ONLY", label: "Discovery only", detail: "CareerPilot finds and analyses jobs, but never prepares or submits on its own." },
  { value: "HUMAN_APPROVED", label: "Human-approved (recommended)", detail: "Anything external needs your explicit approval first." },
  { value: "CONTROLLED", label: "Controlled autonomy", detail: "Agents may act within limits you set. Agents arrive in Phase 8, so today nothing acts on its own." },
];

function Form({ s }: { s: AutomationSettings }) {
  const { update } = useAutomationActions();
  const [mode, setMode] = useState(s.mode);
  const [limit, setLimit] = useState(String(s.daily_submission_limit));
  return (
    <div className="flex flex-col gap-space-md">
      <fieldset className="flex flex-col gap-space-xs">
        <legend className="mb-1 text-[12px] font-medium text-on-surface-variant">Execution mode</legend>
        {modes.map((m) => (
          <label key={m.value} className="flex items-start gap-space-xs text-body-md text-on-surface">
            <input type="radio" name="mode" checked={mode === m.value} onChange={() => setMode(m.value)} className="mt-1" />
            <span>
              {m.label}
              <span className="block text-body-sm text-muted">{m.detail}</span>
            </span>
          </label>
        ))}
      </fieldset>
      <TextInput
        label="Daily submission limit"
        hint="Maximum submission attempts in any 24 hours, including your own."
        type="number"
        min={1}
        max={200}
        value={limit}
        onChange={(e) => setLimit(e.target.value)}
        error={apiFieldError(update.error, "daily_submission_limit")}
      />
      {update.isSuccess && <p role="status" className="text-body-sm text-olive">Saved.</p>}
      <Button className="self-start" disabled={update.isPending} onClick={() => update.mutate({ mode, daily_submission_limit: Number(limit) })}>
        Save automation settings
      </Button>
    </div>
  );
}

/** Mode, daily limit and the global pause (kill switch). */
export function AutomationPanel() {
  const settings = useAutomation();
  const { pause, resume } = useAutomationActions();
  const s = settings.data;
  return (
    <Panel>
      <div id="automation" className="flex flex-col gap-space-md p-space-md">
        <div className="flex items-center justify-between">
          <h2 className="font-serif text-headline-sm text-on-surface">Automation</h2>
          {s && <Tag tone={s.paused ? "terracotta" : "olive"}>{s.paused ? "Paused" : "Active"}</Tag>}
        </div>
        {settings.isError && <p className="text-terracotta">{settings.error.message}</p>}
        {s && (
          <>
            <div className="flex flex-col gap-space-xs rounded-sm border border-stone p-space-sm">
              <p className="text-body-md text-on-surface">
                {s.paused
                  ? `Automation has been paused ${s.paused_at ? relativeTime(s.paused_at) : ""}. Nothing acts on your behalf until you resume.`
                  : "Pausing immediately stops anything acting on your behalf and cancels automated submissions in progress. Your own actions are not affected."}
              </p>
              {s.paused ? (
                <Button variant="secondary" className="self-start" disabled={resume.isPending} onClick={() => resume.mutate()}>
                  Resume automation
                </Button>
              ) : (
                <Button variant="destructive" icon="pause_circle" className="self-start" disabled={pause.isPending} onClick={() => pause.mutate()}>
                  Pause all automation
                </Button>
              )}
            </div>
            <Form key={s.updated_at ?? "default"} s={s} />
          </>
        )}
      </div>
    </Panel>
  );
}
