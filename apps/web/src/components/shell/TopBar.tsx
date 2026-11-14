import { useEffect, useState } from "react";
import { useLocation } from "react-router";
import { ApiError } from "../../lib/api";
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
          <span className="label-caps hidden items-center gap-1.5 rounded-sm bg-surface-container-low px-space-sm py-1 text-on-surface-variant md:inline-flex">
            <span aria-hidden className="h-2 w-2 rounded-full bg-outline-variant" />
            Agent loop: not enabled
          </span>
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
