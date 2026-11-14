import { useEffect, useRef, useState } from "react";
import { Outlet } from "react-router";
import { Icon } from "../ui/Icon";
import { Sidebar } from "./Sidebar";
import { TopBar } from "./TopBar";

/**
 * Stitch app shell: fixed graphite sidebar (≥1024px), sticky top bar.
 * Below 1024px the sidebar becomes a modal drawer (DESIGN.md responsive rules).
 */
export function AppShell() {
  const [menuOpen, setMenuOpen] = useState(false);
  const closeRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!menuOpen) return;
    closeRef.current?.focus();
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setMenuOpen(false);
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [menuOpen]);

  return (
    <div className="min-h-screen bg-surface">
      <a href="#main" className="sr-only focus:not-sr-only focus:fixed focus:left-2 focus:top-2 focus:z-[60] focus:bg-citron focus:px-3 focus:py-2 focus:text-graphite">
        Skip to content
      </a>

      <aside className="fixed inset-y-0 left-0 z-40 hidden w-72 lg:block">
        <Sidebar />
      </aside>

      {menuOpen && (
        <div className="fixed inset-0 z-50 lg:hidden" role="dialog" aria-modal="true" aria-label="Navigation" id="mobile-nav">
          <button type="button" className="absolute inset-0 bg-graphite/40" aria-label="Close navigation" tabIndex={-1} onClick={() => setMenuOpen(false)} />
          <div className="relative h-full w-72 max-w-[85vw] shadow-overlay">
            <button
              ref={closeRef}
              type="button"
              onClick={() => setMenuOpen(false)}
              className="absolute right-2 top-4 z-10 rounded-sm p-1 text-outline-variant hover:text-surface"
              aria-label="Close navigation"
            >
              <Icon name="close" size={20} />
            </button>
            <Sidebar onNavigate={() => setMenuOpen(false)} />
          </div>
        </div>
      )}

      <div className="flex min-h-screen flex-col lg:pl-72">
        <TopBar onOpenMenu={() => setMenuOpen(true)} menuOpen={menuOpen} />
        <main id="main" className="mx-auto w-full max-w-[1440px] flex-1 px-space-md py-space-lg lg:px-margin-desktop">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
