import { useEffect, useState } from "react";
import { Link, useLocation } from "react-router";
import { ApiError } from "../../lib/api";
import { useAutomation } from "../../lib/applications";
import { useNotifications } from "../../lib/notifications";
import { useTriggerDiscovery } from "../../lib/jobs";
import { Button } from "../ui/Button";
import { Icon } from "../ui/Icon";
import { findNavItem } from "./nav";

export function TopBar({ onOpenMenu, menuOpen }: { onOpenMenu: () => void; menuOpen: boolean }) {
  const { pathname } = useLocation();
  const current = findNavItem(pathname);
  const trigger = useTriggerDiscovery();
  const [notice, setNotice] = useState<string | null>(null);
  useEffect(() => {
    if (!notice) return;
    const t = setTimeout(() => setNotice(null), 6000);
    return () => clearTimeout(t);
  }, [notice]);

  return (
    <header className="sticky top-0 z-30 h-16 border-b border-stone bg-surface/90 backdrop-blur">
      <div className="flex h-16 items-center justify-between gap-space-md px-space-md lg:px-space-lg">
        <div className="flex min-w-0 items-center gap-space-sm">
          <button
            type="button"
            className="-ml-1 rounded-sm p-1.5 text-on-surface hover:bg-surface-container lg:hidden"
            onClick={onOpenMenu}
            aria-label="Open navigation"
            aria-expanded={menuOpen}
            aria-controls="mobile-nav"
          >
            <Icon name="menu" size={22} />
          </button>
          <div className="flex min-w-0 items-center gap-2">
            <span className="hidden font-serif text-headline-sm text-on-surface sm:inline">Career Execution</span>
            <span className="hidden text-outline-variant sm:inline" aria-hidden>
              /
            </span>
            <span className="truncate font-mono text-label-md text-on-surface">{current?.title ?? "Not found"}</span>
          </div>
        </div>

        <div className="flex items-center gap-space-sm">
          <AutomationBadge />
          <NotificationBell />
          {notice && (
            <span role="status" className="hidden max-w-xs truncate text-body-sm text-on-surface-variant lg:inline" title={notice}>
              {notice}
            </span>
          )}
          <Button
            icon="bolt"
            iconClassName="text-secondary-fixed"
            disabled={trigger.isPending}
            onClick={() =>
              trigger.mutate(undefined, {
                onSuccess: (r) => setNotice(r.message),
                onError: (e) => setNotice(e instanceof ApiError ? e.message : "Could not start discovery."),
              })
            }
          >
            <span className="hidden sm:inline">{trigger.isPending ? "Queuing…" : "Run Discovery Cycle"}</span>
            <span className="sm:hidden">Discover</span>
          </Button>
        </div>
      </div>
    </header>
  );
}

const modeLabel = { DISCOVERY_ONLY: "Discovery only", HUMAN_APPROVED: "Human approved", CONTROLLED: "Controlled" } as const;

/** The real automation state (mode + pause switch), linking to the Automation Centre. */
function AutomationBadge() {
  const a = useAutomation();
  if (!a.data) return null;
  const paused = a.data.paused;
  return (
    <Link
      to="/automation"
      className="label-caps hidden items-center gap-1.5 rounded-sm bg-surface-container-low px-space-sm py-1 text-on-surface-variant hover:bg-surface-container md:inline-flex"
    >
      <span aria-hidden className={"h-2 w-2 rounded-full " + (paused ? "bg-terracotta" : "bg-olive")} />
      {paused ? "Automation paused" : `Automation: ${modeLabel[a.data.mode]}`}
    </Link>
  );
}

function NotificationBell() {
  const n = useNotifications(1);
  const unread = n.data?.unread_count ?? 0;
  return (
    <Link
      to="/notifications"
      aria-label={unread > 0 ? `Notifications, ${unread} unread` : "Notifications"}
      className="relative rounded-sm p-1.5 text-on-surface hover:bg-surface-container"
    >
      <Icon name="notifications" size={22} />
      {unread > 0 && (
        <span aria-hidden className="absolute -right-0.5 -top-0.5 min-w-[18px] rounded-full bg-on-surface px-1 text-center font-mono text-[11px] leading-[18px] text-surface">
          {unread > 99 ? "99+" : unread}
        </span>
      )}
    </Link>
  );
}
