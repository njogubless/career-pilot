import { Link, useSearchParams } from "react-router";
import { Loading } from "../../components/ui/Loading";
import { Panel } from "../../components/ui/Panel";
import { Tag } from "../../components/ui/Tag";
import { useProfile, type Completeness } from "../../lib/profile";
import { BasicsForm } from "./BasicsForm";
import { CertificationForm, EducationForm, ExperienceForm, ProjectForm } from "./FactForms";
import { Dates, FactList } from "./FactList";
import { PreferencesForm } from "./PreferencesForm";
import { SkillsPanel } from "./SkillsPanel";

function CompletenessPanel({ c }: { c: Completeness }) {
  return (
    <Panel className="p-space-md">
      <div className="flex items-baseline justify-between">
        <span className="label-caps text-outline">Profile completeness</span>
        <span className="font-mono text-data-metric text-on-surface">{c.score}%</span>
      </div>
      <div className="mt-space-sm h-1.5 w-full bg-surface-container-high" role="progressbar" aria-valuenow={c.score} aria-valuemin={0} aria-valuemax={100} aria-label="Profile completeness">
        <div className={c.score >= 90 ? "h-full bg-citron" : "h-full bg-graphite"} style={{ width: `${c.score}%` }} />
      </div>
      {c.missing.length > 0 && (
        <ul className="mt-space-md space-y-space-sm">
          {c.missing.map((m) => (
            <li key={m.key} className="border-l-2 border-terracotta pl-space-sm">
              <p className="text-title-md text-on-surface">{m.label}</p>
              <p className="text-body-sm text-muted">{m.impact}</p>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
}

export function ProfilePage() {
  const profile = useProfile();
  const [params] = useSearchParams();

  if (profile.isPending) return <Loading label="Loading profile" />;
  if (profile.isError)
    return (
      <p role="alert" className="text-terracotta">
        {profile.error.message}
      </p>
    );
  const p = profile.data;

  return (
    <div className="flex flex-col gap-space-lg">
      <header className="flex flex-col gap-space-xs">
        <Tag tone="graphite" className="self-start">
          Dossier & systems
        </Tag>
        <h1 className="font-serif text-headline-lg-mobile text-on-surface md:text-headline-lg">Professional profile</h1>
        <p className="max-w-2xl text-body-lg text-on-surface-variant">
          The facts here are the only material CareerPilot uses for matching and applications. Imported or inferred facts stay marked
          until you confirm them.
        </p>
      </header>

      {params.get("welcome") && (
        <Panel className="flex flex-col gap-space-sm border-citron p-space-md sm:flex-row sm:items-center sm:justify-between">
          <p className="text-body-md text-on-surface">
            Welcome. The fastest start is to upload your CV and review what CareerPilot extracts, or fill in the sections below by hand.
          </p>
          <Link to="/documents" className="shrink-0 rounded-sm bg-graphite px-space-md py-2 text-[14px] font-semibold text-ivory">
            Upload CV
          </Link>
        </Panel>
      )}

      <div className="grid grid-cols-1 items-start gap-space-lg xl:grid-cols-12">
        <div className="flex min-w-0 flex-col gap-space-lg xl:col-span-8">
          <BasicsForm basics={p.basics} />
          <FactList
            kind="experiences"
            title="Experience"
            eyebrow="Career record"
            icon="work_history"
            emptyText="Experience drives seniority and domain matching. Add roles here or import them from your CV."
            items={p.experiences}
            form={(props) => <ExperienceForm {...props} />}
            render={(e) => (
              <>
                <p className="font-serif text-headline-sm text-on-surface">{e.title}</p>
                <p className="text-body-md text-on-surface-variant">
                  {e.employer}
                  {e.location ? ` · ${e.location}` : ""}
                </p>
                <Dates start={e.start_date} end={e.end_date} current={e.is_current} />
                {e.achievements.length > 0 && (
                  <ul className="mt-space-xs list-disc pl-5 text-body-sm text-on-surface-variant">
                    {e.achievements.map((a) => (
                      <li key={a}>{a}</li>
                    ))}
                  </ul>
                )}
              </>
            )}
          />
          <SkillsPanel skills={p.skills} />
          <FactList
            kind="education"
            title="Education"
            eyebrow="Credentials"
            icon="school"
            emptyText="Add degrees, diplomas and relevant courses."
            items={p.education}
            form={(props) => <EducationForm {...props} />}
            render={(e) => (
              <>
                <p className="text-title-md text-on-surface">{e.institution}</p>
                <p className="text-body-sm text-on-surface-variant">{[e.degree, e.field].filter(Boolean).join(", ")}</p>
                <Dates start={e.start_date} end={e.end_date} />
              </>
            )}
          />
          <FactList
            kind="certifications"
            title="Certifications"
            eyebrow="Credentials"
            icon="workspace_premium"
            emptyText="Add certifications you hold. Only verified ones are used in applications."
            items={p.certifications}
            form={(props) => <CertificationForm {...props} />}
            render={(c) => (
              <>
                <p className="text-title-md text-on-surface">{c.name}</p>
                <p className="text-body-sm text-on-surface-variant">{c.issuer}</p>
              </>
            )}
          />
          <FactList
            kind="projects"
            title="Projects"
            eyebrow="Evidence"
            icon="deployed_code"
            emptyText="Projects provide evidence for skills and domain relevance."
            items={p.projects}
            form={(props) => <ProjectForm {...props} />}
            render={(pr) => (
              <>
                <p className="text-title-md text-on-surface">{pr.name}</p>
                {pr.description && <p className="text-body-sm text-on-surface-variant">{pr.description}</p>}
                {pr.url && (
                  <a href={pr.url} target="_blank" rel="noopener noreferrer nofollow" className="text-body-sm underline underline-offset-4">
                    {pr.url}
                  </a>
                )}
              </>
            )}
          />
        </div>
        <div className="flex flex-col gap-space-lg xl:sticky xl:top-20 xl:col-span-4">
          <CompletenessPanel c={p.completeness} />
          <PreferencesForm prefs={p.preferences} />
        </div>
      </div>
    </div>
  );
}
