import clsx from "clsx";
import type { ReactNode } from "react";

export type TagTone = "citron" | "graphite" | "stone" | "olive" | "terracotta" | "outline";

const tones: Record<TagTone, string> = {
  citron: "bg-citron text-graphite",
  graphite: "bg-graphite text-secondary-container",
  stone: "bg-stone text-graphite",
  olive: "bg-olive text-white",
  terracotta: "border border-terracotta text-terracotta",
  outline: "border border-stone text-on-surface-variant",
};

/** Compact uppercase monospaced status tag, 2px radius (DESIGN.md: Status Tags). */
export function Tag({ tone = "outline", children, className }: { tone?: TagTone; children: ReactNode; className?: string }) {
  return (
    <span className={clsx("label-caps inline-flex shrink-0 items-center gap-1 whitespace-nowrap rounded-xs px-1.5 py-0.5", tones[tone], className)}>
      {children}
    </span>
  );
}
