/** Only allow same-app relative redirects (prevents open redirects via ?next=). */
export function safeNext(next: string | null): string {
  return next && next.startsWith("/") && !next.startsWith("//") && !next.startsWith("/\\") ? next : "/";
}

/** API date (YYYY-MM-DD) → <input type="month"> value (YYYY-MM). */
export const toMonth = (d: string | null | undefined) => (d ? d.slice(0, 7) : "");
/** <input type="month"> value → API date, or null. */
export const fromMonth = (m: string | null | undefined) => (m ? `${m}-01` : null);
