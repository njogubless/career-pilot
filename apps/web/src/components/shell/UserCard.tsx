import { useNavigate } from "react-router";
import { useLogout, useSession } from "../../lib/session";
import { Icon } from "../ui/Icon";

/** Sidebar footer user card (Stitch shell), with sign out. */
export function UserCard() {
  const session = useSession();
  const logout = useLogout();
  const navigate = useNavigate();
  const user = session.data;
  if (!user) return null;
  const initials = user.display_name
    .split(/\s+/)
    .slice(0, 2)
    .map((p) => p[0]?.toUpperCase())
    .join("");

  return (
    <div className="flex items-center gap-space-sm bg-surface-container-high/10 p-space-sm">
      <span aria-hidden className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-surface/15 font-mono text-label-md text-surface">
        {initials}
      </span>
      <div className="min-w-0 flex-1">
        <p className="truncate text-title-md text-surface">{user.display_name}</p>
        <p className="label-caps truncate text-outline-variant">{user.email}</p>
      </div>
      <button
        type="button"
        onClick={() => logout.mutate(undefined, { onSettled: () => navigate("/login") })}
        disabled={logout.isPending}
        className="rounded-sm p-1.5 text-outline-variant hover:bg-surface/10 hover:text-surface"
        aria-label="Sign out"
        title="Sign out"
      >
        <Icon name="logout" size={18} />
      </button>
    </div>
  );
}
