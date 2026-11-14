import clsx from "clsx";

type Props = { name: string; size?: number; className?: string; label?: string };

/** Material Symbols Outlined glyph (self-hosted). Decorative unless `label` is set. */
export function Icon({ name, size = 18, className, label }: Props) {
  return (
    <span
      className={clsx("material-symbols-outlined", className)}
      style={{ fontSize: size }}
      aria-hidden={label ? undefined : true}
      aria-label={label}
      role={label ? "img" : undefined}
    >
      {name}
    </span>
  );
}
