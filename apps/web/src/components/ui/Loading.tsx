/** Quiet loading indicator (no spinners with glow; a mono status line). */
export function Loading({ label = "Loading" }: { label?: string }) {
  return (
    <p role="status" aria-live="polite" className="label-caps flex items-center gap-2 py-space-md text-outline">
      <span aria-hidden className="h-1.5 w-1.5 animate-pulse rounded-full bg-olive" />
      {label}…
    </p>
  );
}
