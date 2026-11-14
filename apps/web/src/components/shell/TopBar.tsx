import { useLocation } from "react-router";
import { Button } from "../ui/Button";
import { Icon } from "../ui/Icon";
import { findNavItem } from "./nav";

export function TopBar({ onOpenMenu, menuOpen }: { onOpenMenu: () => void; menuOpen: boolean }) {
  const { pathname } = useLocation();
  const current = findNavItem(pathname);

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
          {/* Disabled until job discovery exists (Phase 3): never a button that silently does nothing. */}
          <Button icon="bolt" iconClassName="text-secondary-fixed" disabled title="Available once job discovery is enabled (Phase 3)">
            <span className="hidden sm:inline">Run Discovery Cycle</span>
            <span className="sm:hidden">Discover</span>
          </Button>
        </div>
      </div>
    </header>
  );
}
