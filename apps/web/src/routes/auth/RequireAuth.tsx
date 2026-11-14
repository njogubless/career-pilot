import type { ReactNode } from "react";
import { Navigate, useLocation } from "react-router";
import { Loading } from "../../components/ui/Loading";
import { useSession } from "../../lib/session";

export function RequireAuth({ children }: { children: ReactNode }) {
  const session = useSession();
  const loc = useLocation();

  if (session.isPending) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-surface">
        <Loading label="Checking your session" />
      </div>
    );
  }
  if (session.isError) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-surface p-space-md">
        <p role="alert" className="max-w-md text-body-md text-terracotta">
          {session.error.message}
        </p>
      </div>
    );
  }
  if (!session.data) {
    return <Navigate to={`/login?next=${encodeURIComponent(loc.pathname + loc.search)}`} replace />;
  }
  return <>{children}</>;
}
