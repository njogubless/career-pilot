import clsx from "clsx";
import { NavLink } from "react-router";
import { Icon } from "../ui/Icon";
import { navGroups } from "./nav";
import { SystemStatusCard } from "./SystemStatusCard";
import { UserCard } from "./UserCard";

export function Sidebar({ onNavigate }: { onNavigate?: () => void }) {
  return (
    <div className="flex h-full flex-col bg-primary-container text-surface">
      <div className="flex h-16 items-center justify-between px-space-md">
        <div className="flex items-center gap-space-sm">
          <span aria-hidden className="flex h-8 w-8 items-center justify-center rounded-sm bg-surface font-serif text-headline-sm text-graphite">
            C
          </span>
          <span className="font-serif text-headline-sm tracking-tight">CareerPilot</span>
        </div>
        <span className="label-caps rounded-sm bg-surface-container-high/20 px-space-xs py-0.5 text-outline-variant">AI</span>
      </div>

      <div className="px-space-md py-space-sm">
        <SystemStatusCard />
      </div>

      <nav aria-label="Primary" className="flex-1 space-y-0.5 overflow-y-auto px-space-sm py-space-sm">
        {navGroups.map((group, gi) => (
          <div key={group.label}>
            <p className={clsx("label-caps px-space-sm pb-1 text-outline-variant/70", gi === 0 ? "pt-space-xs" : "pt-space-md")}>
              {group.label}
            </p>
            <ul>
              {group.items.map((item) => (
                <li key={item.path}>
                  <NavLink
                    to={item.path}
                    end={item.path === "/"}
                    onClick={onNavigate}
                    className={({ isActive }) =>
                      clsx(
                        "group flex items-center justify-between rounded-sm px-space-sm py-2 transition-colors",
                        isActive
                          ? "bg-surface-container-lowest font-semibold text-primary"
                          : "text-primary-fixed-dim hover:bg-surface/10 hover:text-surface",
                      )
                    }
                  >
                    {({ isActive }) => (
                      <>
                        <span className="flex items-center gap-space-sm">
                          <Icon name={item.icon} className={isActive ? "text-graphite" : "text-outline-variant group-hover:text-surface"} />
                          <span className="text-title-md">{item.label}</span>
                        </span>
                        {!item.built && (
                          <span className={clsx("label-caps", isActive ? "text-outline" : "text-outline-variant/70")}>
                            Phase {item.phase}
                          </span>
                        )}
                      </>
                    )}
                  </NavLink>
                </li>
              ))}
            </ul>
          </div>
        ))}
      </nav>
      <UserCard />
    </div>
  );
}
