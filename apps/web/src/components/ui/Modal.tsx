import { useEffect, useId, useRef, type ReactNode } from "react";
import { Icon } from "./Icon";

/**
 * Top-tier overlay (DESIGN.md): white surface, graphite hairline at 20%,
 * functional shadow. Traps focus loosely (initial focus + Escape) and
 * restores focus on close.
 */
export function Modal({ title, onClose, children, footer }: { title: string; onClose: () => void; children: ReactNode; footer?: ReactNode }) {
  const titleId = useId();
  const panelRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    const first = panelRef.current?.querySelector<HTMLElement>("input, select, textarea, button:not([data-close])");
    (first ?? panelRef.current)?.focus();
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("keydown", onKey);
      previous?.focus();
    };
  }, [onClose]);

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-graphite/40 p-space-md sm:items-center">
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
        className="w-full max-w-xl rounded-sm border border-graphite/20 bg-surface-container-lowest shadow-overlay"
      >
        <div className="flex items-center justify-between border-b border-stone px-space-md py-space-sm">
          <h2 id={titleId} className="font-serif text-headline-sm text-on-surface">
            {title}
          </h2>
          <button type="button" data-close onClick={onClose} className="rounded-sm p-1 text-outline hover:text-on-surface" aria-label="Close">
            <Icon name="close" size={20} />
          </button>
        </div>
        <div className="max-h-[70vh] overflow-y-auto p-space-md">{children}</div>
        {footer && <div className="flex justify-end gap-space-sm border-t border-stone px-space-md py-space-sm">{footer}</div>}
      </div>
    </div>
  );
}
