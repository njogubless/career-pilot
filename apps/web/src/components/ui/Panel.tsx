import clsx from "clsx";
import type { ReactNode } from "react";

/** Mid-tier dossier panel: white, 1px soft-stone hairline, 4px radius. Borders over shadows. */
export function Panel({ children, className, as: As = "section" }: { children: ReactNode; className?: string; as?: "section" | "article" | "div" }) {
  return <As className={clsx("rounded-sm border border-stone bg-surface-container-lowest", className)}>{children}</As>;
}

/** Editorial section header: uppercase mono eyebrow + Newsreader title. */
export function SectionHeader({ eyebrow, title, actions }: { eyebrow?: string; title: string; actions?: ReactNode }) {
  return (
    <div className="flex flex-col justify-between gap-space-sm sm:flex-row sm:items-end">
      <div>
        {eyebrow && <p className="label-caps text-outline">{eyebrow}</p>}
        <h2 className="font-serif text-headline-md text-on-surface">{title}</h2>
      </div>
      {actions && <div className="flex shrink-0 items-center gap-space-sm">{actions}</div>}
    </div>
  );
}

/** Honest empty state: says what is missing and what will populate it. */
export function EmptyState({ icon, title, children, action }: { icon: string; title: string; children: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-start gap-space-sm border-t border-stone p-space-lg">
      <span className="material-symbols-outlined text-olive" aria-hidden style={{ fontSize: 24 }}>
        {icon}
      </span>
      <h3 className="font-serif text-headline-sm text-on-surface">{title}</h3>
      <div className="max-w-xl text-body-md text-on-surface-variant">{children}</div>
      {action}
    </div>
  );
}
