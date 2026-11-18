import { useState } from "react";
import { useNavigate } from "react-router";
import { Button } from "../../components/ui/Button";
import { TextArea, TextInput } from "../../components/ui/Form";
import { Panel } from "../../components/ui/Panel";
import { Tag } from "../../components/ui/Tag";
import { apiFieldError } from "../../lib/api";
import { useMeta } from "../../lib/meta";
import { ScheduleThisPlan } from "./Schedules";
import { agentTitle, defaultParams, useInterpret, useStartWorkflow, useTemplates, type Params, type TemplateName } from "../../lib/workflows";

function PlanPreview({ template }: { template: TemplateName }) {
  const templates = useTemplates();
  const t = templates.data?.data.find((x) => x.name === template);
  if (!t) return null;
  return (
    <div className="flex flex-col gap-space-xs">
      <p className="label-caps text-outline">Plan the agents will follow ({templates.data?.plan_version})</p>
      <ol className="flex flex-col gap-1">
        {t.tasks.map((s, i) => (
          <li key={s.type} className="flex flex-wrap items-baseline gap-space-xs text-body-sm">
            <span className="font-mono text-label-sm text-muted">{String(i + 1).padStart(2, "0")}</span>
            <span className="text-on-surface">{s.title}</span>
            <Tag tone={s.approval ? "citron" : "outline"}>{s.approval ? "Your approval" : agentTitle[s.agent]}</Tag>
          </li>
        ))}
      </ol>
    </div>
  );
}

/** Describe an objective; review the validated plan; start it. Nothing runs before you press Start. */
export function ObjectiveComposer() {
  const navigate = useNavigate();
  const meta = useMeta();
  const llm = meta.data?.integrations.llm?.status === "available";
  const interpret = useInterpret();
  const start = useStartWorkflow();
  const [objective, setObjective] = useState("");
  const [template, setTemplate] = useState<TemplateName>("DISCOVER_PREPARE_APPROVE");
  const [p, setP] = useState<Params>(defaultParams);
  const [source, setSource] = useState<"STRUCTURED" | "AI">("STRUCTURED");
  const [salary, setSalary] = useState({ amount: "", currency: "USD" });
  const err = (f: string) => apiFieldError(start.error, f) ?? apiFieldError(start.error, `params.${f}`);

  const runInterpret = () =>
    interpret.mutate(objective, {
      onSuccess: (r) => {
        setTemplate(r.proposal.template.name);
        setP(r.proposal.params);
        setSalary(r.proposal.params.min_monthly_salary ? { amount: String(r.proposal.params.min_monthly_salary.amount), currency: r.proposal.params.min_monthly_salary.currency } : { amount: "", currency: "USD" });
        setSource("AI");
      },
    });

  const planParams = (): Params => ({ ...p, min_monthly_salary: salary.amount ? { amount: Number(salary.amount), currency: salary.currency.toUpperCase() } : null });
  const submit = () => {
    const params = planParams();
    start.mutate({ objective, template, params, interpretation: source }, { onSuccess: (w) => navigate(`/intelligence/workflows/${w.id}`) });
  };

  return (
    <Panel>
      <div className="border-b border-stone p-space-md">
        <p className="label-caps text-outline">New objective</p>
        <h2 className="font-serif text-headline-sm text-on-surface">What should the agents work on?</h2>
      </div>
      <div className="grid grid-cols-1 gap-space-lg p-space-md lg:grid-cols-2">
        <div className="flex flex-col gap-space-sm">
          <TextArea
            label="Your objective"
            rows={4}
            maxLength={2000}
            value={objective}
            onChange={(e) => setObjective(e.target.value)}
            placeholder="Find five remote backend engineering jobs accepting Kenyan applicants, paying at least $4,000 monthly where disclosed. Prepare applications and ask me to approve them."
          />
          <div className="flex flex-wrap items-center gap-space-sm">
            <Button variant="secondary" icon="auto_awesome" disabled={!llm || objective.trim().length < 3 || interpret.isPending} onClick={runInterpret}>
              {interpret.isPending ? "Interpreting…" : "Interpret with AI"}
            </Button>
            {meta.data && !llm && <span className="text-body-sm text-muted">No AI provider configured: fill in the plan below yourself.</span>}
          </div>
          {interpret.isError && <p className="text-body-sm text-terracotta">{interpret.error.message}</p>}
          {interpret.data && (
            <div className="rounded-sm border border-stone bg-surface-container-low p-space-sm text-body-sm">
              <p className="text-on-surface">Understood as: {interpret.data.summary}</p>
              {interpret.data.questions.map((q) => (
                <p key={q} className="text-terracotta">
                  {q}
                </p>
              ))}
              <p className="font-mono text-label-sm text-muted">Proposed by {interpret.data.prompt_version}. Check the plan before starting.</p>
            </div>
          )}
          <PlanPreview template={template} />
        </div>
        <div className="flex flex-col gap-space-sm">
          <fieldset className="flex flex-col gap-1">
            <legend className="mb-1 text-[12px] font-medium text-on-surface-variant">Workflow</legend>
            {(
              [
                ["DISCOVER_AND_RANK", "Find and shortlist jobs"],
                ["DISCOVER_PREPARE_APPROVE", "Find, prepare CVs and ask me to approve"],
              ] as const
            ).map(([v, label]) => (
              <label key={v} className="flex items-center gap-space-xs text-body-md text-on-surface">
                <input type="radio" name="template" checked={template === v} onChange={() => setTemplate(v)} />
                {label}
              </label>
            ))}
          </fieldset>
          <TextInput label="Role or keywords" value={p.query} maxLength={200} onChange={(e) => setP({ ...p, query: e.target.value })} error={err("query")} />
          <div className="grid grid-cols-2 gap-space-sm">
            <TextInput label="Country you can work in (ISO code)" value={p.country} maxLength={2} onChange={(e) => setP({ ...p, country: e.target.value.toUpperCase() })} error={err("country")} />
            <TextInput label="Jobs to shortlist (1–10)" type="number" min={1} max={10} value={p.count} onChange={(e) => setP({ ...p, count: Number(e.target.value) })} error={err("count")} />
            <TextInput label="Minimum monthly salary" type="number" min={0} value={salary.amount} onChange={(e) => setSalary({ ...salary, amount: e.target.value })} error={err("min_monthly_salary.amount")} />
            <TextInput label="Currency" value={salary.currency} maxLength={3} onChange={(e) => setSalary({ ...salary, currency: e.target.value.toUpperCase() })} error={err("min_monthly_salary.currency")} />
            <TextInput label="Minimum match score (0–100)" type="number" min={0} max={100} value={p.min_score} onChange={(e) => setP({ ...p, min_score: Number(e.target.value) })} error={err("min_score")} />
            <TextInput label="Posted within (days)" type="number" min={1} max={90} value={p.posted_within_days} onChange={(e) => setP({ ...p, posted_within_days: Number(e.target.value) })} error={err("posted_within_days")} />
          </div>
          {(
            [
              ["remote_only", "Remote jobs only"],
              ["require_disclosed_salary", "Exclude jobs that don't publish a salary"],
            ] as const
          ).map(([k, label]) => (
            <label key={k} className="flex items-center gap-space-xs text-body-md text-on-surface">
              <input type="checkbox" checked={p[k]} onChange={(e) => setP({ ...p, [k]: e.target.checked })} />
              {label}
            </label>
          ))}
          {template === "DISCOVER_PREPARE_APPROVE" && (
            <label className="flex items-center gap-space-xs text-body-md text-on-surface">
              <input type="checkbox" checked={p.use_ai} disabled={!llm} onChange={(e) => setP({ ...p, use_ai: e.target.checked })} />
              Draft CVs with AI (otherwise from your profile as written)
            </label>
          )}
          {start.isError && !Object.keys((start.error as { details?: object }).details ?? {}).length && (
            <p role="alert" className="text-body-sm text-terracotta">
              {start.error.message}
            </p>
          )}
          <Button icon="play_arrow" iconClassName="text-secondary-fixed" className="self-start" disabled={start.isPending} onClick={submit}>
            Start workflow
          </Button>
          <ScheduleThisPlan objective={objective} template={template} params={planParams()} />
          <p className="text-body-sm text-muted">
            Agents only search, score, track and prepare. Approving packages is yours, and you always submit on the employer's official site.
          </p>
        </div>
      </div>
    </Panel>
  );
}
