import clsx from "clsx";
import type { ButtonHTMLAttributes, ReactNode } from "react";
import { Icon } from "./Icon";

type Variant = "primary" | "accent" | "secondary" | "destructive" | "ghost";

const variants: Record<Variant, string> = {
  // Deep graphite, ivory text; hover reveals a citron hairline (DESIGN.md: Primary Action).
  primary: "bg-graphite text-ivory hover:outline hover:outline-1 hover:outline-offset-2 hover:outline-citron",
  // Electric citron: reserved for critical high-yield moments.
  accent: "bg-citron text-graphite hover:bg-secondary-fixed-dim",
  secondary: "bg-surface-container-lowest text-graphite border border-stone hover:border-graphite",
  destructive: "bg-transparent text-terracotta border border-terracotta hover:bg-terracotta hover:text-white",
  ghost: "bg-transparent text-on-surface-variant hover:text-on-surface hover:bg-surface-container",
};

type Props = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: Variant;
  icon?: string;
  iconClassName?: string;
  children: ReactNode;
};

export function Button({ variant = "primary", icon, iconClassName, className, children, type = "button", ...rest }: Props) {
  return (
    <button
      type={type}
      className={clsx(
        "inline-flex items-center justify-center gap-2 rounded-sm px-space-md py-2 font-sans text-[14px] font-semibold transition-colors",
        "disabled:cursor-not-allowed disabled:opacity-50 disabled:hover:outline-none",
        variants[variant],
        className,
      )}
      {...rest}
    >
      {icon && <Icon name={icon} size={16} className={iconClassName} />}
      {children}
    </button>
  );
}
