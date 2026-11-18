import { useState } from "react";
import { Button } from "../../components/ui/Button";
import { TextInput } from "../../components/ui/Form";
import { Loading } from "../../components/ui/Loading";
import { Panel } from "../../components/ui/Panel";
import { Tag, type TagTone } from "../../components/ui/Tag";
import { apiFieldError } from "../../lib/api";
import { relativeTime } from "../../lib/format";
import { useScheduleActions, useSchedules, type Params, type Schedule, type ScheduleBody, type TemplateName } from "../../lib/workflows";

const weekdays = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

const outcome: Record<NonNullable<Schedule["last_outcome"]>, [string, TagTone]> = {
  STARTED: ["Started", "olive"],
  SKIPPED_PAUSED: ["Skipped: automation paused", "terracotta"],
  SKIPPED_OVERLAP: ["Skipped: previous run still open", "stone"],
  SKIPPED_LIMIT: ["Skipped: limit reached", "terracotta"],
  FAILED: ["Failed", "terracotta"],
};

const localZone = () => {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
};

function cadenceText(s: Pick<Schedule, "cadence" | "weekday" | "hour" | "timezone">) {
  const at = `${String(s.hour).padStart(2, "0")}:00 ${s.timezone}`;
  return s.cadence === "DAILY" ? `Daily at ${at}` : `Every ${weekdays[s.weekday ?? 1]} at ${at}`;
}

/** Saves the composer's current plan as a recurring run. Scheduled runs keep every approval step. */
export function ScheduleThisPlan({ objective, template, params }: { objective: string; template: TemplateName; params: Params }) {
  const { create } = useScheduleActions();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [cadence, setCadence] = useState<"DAILY" | "WEEKLY">("WEEKLY");
  const [weekday, setWeekday] = useState(1);
  const [hour, setHour] = useState(8);
  const [timezone, setTimezone] = useState(localZone);
  const err = (f: string) => apiFieldError(create.error, f) ?? apiFieldError(create.error, `params.${f}`);
  if (!open)
    return (
      <Button variant="secondary" icon="event_repeat" className="self-start" onClick={() => setOpen(true)}>
        Repeat this plan on a schedule
      </Button>
    );
  const body: ScheduleBody = { name: name.trim(), objective, template, params, cadence, weekday: cadence === "WEEKLY" ? weekday : null, hour, timezone, enabled: true };
  return (
    <div className="flex flex-col gap-space-sm rounded-sm border border-stone p-space-sm">
      <p className="text-title-md text-on-surface">Repeat on a schedule</p>
      <TextInput label="Schedule name" maxLength={120} value={name} onChange={(e) => setName(e.target.value)} error={err("name")} />
      <div className="grid grid-cols-2 gap-space-sm">
        <label className="flex flex-col gap-1 text-[12px] font-medium text-on-surface-variant">
          How often
          <select value={cadence} onChange={(e) => setCadence(e.target.value as "DAILY" | "WEEKLY")} className="rounded-sm border border-stone bg-surface-container-lowest px-2 py-1.5 text-body-md text-on-surface">
            <option value="DAILY">Daily</option>
            <option value="WEEKLY">Weekly</option>
          </select>
        </label>
        {cadence === "WEEKLY" && (
          <label className="flex flex-col gap-1 text-[12px] font-medium text-on-surface-variant">
            Day
            <select value={weekday} onChange={(e) => setWeekday(Number(e.target.value))} className="rounded-sm border border-stone bg-surface-container-lowest px-2 py-1.5 text-body-md text-on-surface">
              {weekdays.map((d, i) => (
                <option key={d} value={i}>
                  {d}
                </option>
              ))}
            </select>
          </label>
        )}
        <TextInput label="Hour (0–23)" type="number" min={0} max={23} value={hour} onChange={(e) => setHour(Number(e.target.value))} error={err("hour")} />
        <TextInput label="Time zone" value={timezone} onChange={(e) => setTimezone(e.target.value)} error={err("timezone")} />
      </div>
      {create.isError && !Object.keys((create.error as { details?: object }).details ?? {}).length && (
        <p role="alert" className="text-body-sm text-terracotta">
          {create.error.message}
        </p>
      )}
      {create.isSuccess ? (
        <p role="status" className="text-body-sm text-olive">
          Scheduled. Next run {new Date(create.data.next_run_at).toLocaleString()}.
        </p>
      ) : (
        <div className="flex gap-space-sm">
          <Button disabled={create.isPending} onClick={() => create.mutate(body)}>
            Save schedule
          </Button>
          <Button variant="ghost" onClick={() => setOpen(false)}>
            Cancel
          </Button>
        </div>
      )}
      <p className="text-body-sm text-muted">
        A scheduled run is skipped while automation is paused or while the previous run from this schedule is still waiting for you. Approvals are
        still required every time.
      </p>
    </div>
  );
}

export function SchedulesPanel() {
  const q = useSchedules();
  const { update, remove } = useScheduleActions();
  const list = q.data?.data ?? [];
  const toggle = (s: Schedule) =>
    update.mutate({
      id: s.id,
      body: { name: s.name, objective: s.objective, template: s.template, params: s.params, cadence: s.cadence, weekday: s.weekday, hour: s.hour, timezone: s.timezone, enabled: !s.enabled },
    });
  return (
    <Panel>
      <div className="border-b border-stone p-space-md">
        <p className="label-caps text-outline">Scheduled objectives</p>
        <h2 className="font-serif text-headline-sm text-on-surface">Schedules</h2>
      </div>
      {q.isPending && <Loading />}
      {q.data && list.length === 0 && (
        <p className="p-space-md text-body-sm text-muted">No schedules. Build a plan above and choose "Repeat this plan on a schedule" to run it daily or weekly.</p>
      )}
      {(update.isError || remove.isError) && <p className="px-space-md pt-space-sm text-body-sm text-terracotta">{(update.error ?? remove.error)?.message}</p>}
      <ul className="divide-y divide-stone">
        {list.map((s) => (
          <li key={s.id} className="flex flex-wrap items-center justify-between gap-space-sm px-space-md py-space-sm">
            <div className="flex min-w-0 flex-col">
              <span className="truncate text-body-md text-on-surface">{s.name}</span>
              <span className="font-mono text-label-sm text-muted">
                {cadenceText(s)} · {s.enabled ? `next ${new Date(s.next_run_at).toLocaleString()}` : "disabled"}
                {s.last_run_at ? ` · last ${relativeTime(s.last_run_at)}` : ""}
              </span>
            </div>
            <div className="flex flex-wrap items-center gap-space-sm">
              {s.last_outcome && (
                <span title={s.last_message ?? undefined}>
                  <Tag tone={outcome[s.last_outcome][1]}>{outcome[s.last_outcome][0]}</Tag>
                </span>
              )}
              <Button variant="secondary" disabled={update.isPending} onClick={() => toggle(s)}>
                {s.enabled ? "Disable" : "Enable"}
              </Button>
              <Button
                variant="destructive"
                disabled={remove.isPending}
                onClick={() => {
                  if (window.confirm(`Delete the schedule "${s.name}"? Past runs are kept.`)) remove.mutate(s.id);
                }}
              >
                Delete
              </Button>
            </div>
          </li>
        ))}
      </ul>
    </Panel>
  );
}
