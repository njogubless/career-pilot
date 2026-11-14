import type { ReactNode } from "react";

const principles = [
  "Every claim in your applications traces to a fact you confirmed.",
  "Nothing is sent or submitted without your approval.",
  "Unknown stays unknown: no invented salaries or eligibility.",
];

/** Auth screens extend the Stitch system (ADR-012): graphite brand column, ivory form column. */
export function AuthLayout({ eyebrow, title, children, footer }: { eyebrow: string; title: string; children: ReactNode; footer?: ReactNode }) {
  return (
    <div className="grid min-h-screen bg-surface lg:grid-cols-[minmax(0,5fr)_minmax(0,7fr)]">
      <aside className="hidden flex-col justify-between bg-primary-container p-margin-desktop text-surface lg:flex">
        <div className="flex items-center gap-space-sm">
          <span aria-hidden className="flex h-8 w-8 items-center justify-center rounded-sm bg-surface font-serif text-headline-sm text-graphite">
            C
          </span>
          <span className="font-serif text-headline-sm">CareerPilot</span>
        </div>
        <div>
          <p className="label-caps text-secondary-fixed">Controlled career execution</p>
          <h2 className="mt-space-sm max-w-md font-serif text-headline-lg">Relevant roles. Truthful applications. Your approval at every step.</h2>
          <ul className="mt-space-lg space-y-space-sm border-t border-outline/40 pt-space-md">
            {principles.map((p, i) => (
              <li key={p} className="flex gap-space-sm text-body-md text-primary-fixed-dim">
                <span className="font-mono text-label-md text-secondary-fixed">{String(i + 1).padStart(2, "0")}</span>
                {p}
              </li>
            ))}
          </ul>
        </div>
        <p className="text-body-sm text-on-primary-container">Match scores are decision support, not predictions of hiring outcomes.</p>
      </aside>

      <main className="flex items-center justify-center px-space-md py-space-xl">
        <div className="w-full max-w-md">
          <div className="mb-space-xl flex items-center gap-space-sm lg:hidden">
            <span aria-hidden className="flex h-8 w-8 items-center justify-center rounded-sm bg-graphite font-serif text-headline-sm text-ivory">
              C
            </span>
            <span className="font-serif text-headline-sm text-on-surface">CareerPilot</span>
          </div>
          <p className="label-caps text-outline">{eyebrow}</p>
          <h1 className="mt-1 font-serif text-headline-lg-mobile text-on-surface md:text-headline-lg">{title}</h1>
          <div className="mt-space-lg rounded-sm border border-stone bg-surface-container-lowest p-space-lg">{children}</div>
          {footer && <div className="mt-space-md text-body-md text-on-surface-variant">{footer}</div>}
        </div>
      </main>
    </div>
  );
}
