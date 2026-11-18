import { Loading } from "../../components/ui/Loading";
import { Panel } from "../../components/ui/Panel";
import { Tag } from "../../components/ui/Tag";
import { useRegistry } from "../../lib/workflows";
import { AutomationPanel } from "../settings/AutomationPanel";

const effectLabel: Record<string, string> = { READ: "Reads", INTERNAL: "Writes your records", EXTERNAL: "Acts outside CareerPilot" };

/** Automation Centre: the controls, and exactly what each agent is allowed to do. */
export function AutomationCentrePage() {
  const reg = useRegistry();
  return (
    <div className="flex flex-col gap-space-lg">
      <header className="flex max-w-3xl flex-col gap-space-xs">
        <p className="label-caps text-outline">Dossier & systems</p>
        <h1 className="font-serif text-headline-lg-mobile text-on-surface md:text-headline-lg">Automation Centre</h1>
        <p className="text-body-lg text-on-surface-variant">
          Agents can only use the tools listed here. Permissions are checked by the platform immediately before every action, never by the AI, and
          the pause switch stops everything at once.
        </p>
      </header>
      <div className="grid grid-cols-1 items-start gap-space-lg xl:grid-cols-12">
        <div className="xl:col-span-5">
          <AutomationPanel />
        </div>
        <Panel className="xl:col-span-7">
          <div className="border-b border-stone p-space-md">
            <p className="label-caps text-outline">Agent registry</p>
            <h2 className="font-serif text-headline-sm text-on-surface">Agents and their tools</h2>
          </div>
          {reg.isPending && <Loading />}
          <ul className="divide-y divide-stone">
            {reg.data?.agents.map((a) => (
              <li key={a.name} className="flex flex-col gap-space-xs px-space-md py-space-sm">
                <span className="font-semibold text-on-surface">{a.title}</span>
                <span className="text-body-sm text-on-surface-variant">{a.description}</span>
                <ul className="flex flex-col gap-1">
                  {a.tools.map((name) => {
                    const t = reg.data!.tools.find((x) => x.name === name);
                    return (
                      <li key={name} className="flex flex-wrap items-center gap-space-xs text-body-sm">
                        <span className="font-mono text-label-md text-on-surface">{name}</span>
                        {t && <Tag tone={t.effect === "READ" ? "outline" : "stone"}>{effectLabel[t.effect]}</Tag>}
                        {t?.permission && <Tag tone="citron">Needs {t.permission}</Tag>}
                        {t && (
                          <span className="font-mono text-label-sm text-muted">
                            {t.timeout_seconds}s · {t.max_attempts} attempt(s)
                          </span>
                        )}
                      </li>
                    );
                  })}
                </ul>
              </li>
            ))}
          </ul>
          <p className="border-t border-stone p-space-md text-body-sm text-muted">
            No agent can send email or submit applications. Emails are sent only after you approve each one, and you submit applications yourself on
            the employer's site.
          </p>
        </Panel>
      </div>
    </div>
  );
}
