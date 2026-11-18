import { Link } from "react-router";
import { Button } from "../../components/ui/Button";
import { Icon } from "../../components/ui/Icon";
import { Loading } from "../../components/ui/Loading";
import { EmptyState, Panel } from "../../components/ui/Panel";
import { relativeTime } from "../../lib/format";
import { kindIcon, useNotificationActions, useNotifications } from "../../lib/notifications";

/** In-app notifications. Each one is derived once from a stored record; nothing is sent by email. */
export function NotificationsPage() {
  const q = useNotifications(50);
  const { read, readAll } = useNotificationActions();
  const list = q.data?.data ?? [];
  const unread = q.data?.unread_count ?? 0;
  return (
    <div className="flex max-w-3xl flex-col gap-space-lg">
      <header className="flex flex-wrap items-end justify-between gap-space-sm">
        <div className="flex flex-col gap-space-xs">
          <p className="label-caps text-outline">Inbox</p>
          <h1 className="font-serif text-headline-lg-mobile text-on-surface md:text-headline-lg">Notifications</h1>
          <p className="text-body-md text-on-surface-variant">
            {unread > 0 ? `${unread} unread.` : "All caught up."} Choose what you hear about in{" "}
            <Link to="/settings" className="underline">
              Settings
            </Link>
            .
          </p>
        </div>
        <Button variant="secondary" icon="done_all" disabled={unread === 0 || readAll.isPending} onClick={() => readAll.mutate()}>
          Mark all as read
        </Button>
      </header>
      {q.isPending && <Loading label="Loading notifications" />}
      {q.isError && <p role="alert" className="text-terracotta">{q.error.message}</p>}
      {q.data && list.length === 0 && (
        <Panel>
          <EmptyState icon="notifications" title="No notifications yet">
            You will be told about strong new matches, approvals waiting for you, recruiter replies, follow-ups that are ready and weekly reports.
          </EmptyState>
        </Panel>
      )}
      {list.length > 0 && (
        <Panel>
          <ul className="divide-y divide-stone">
            {list.map((n) => (
              <li key={n.id} className={"flex gap-space-sm p-space-md " + (n.read_at ? "" : "bg-surface-container-low")}>
                <Icon name={kindIcon[n.kind] ?? "notifications"} size={20} className="mt-0.5 shrink-0 text-on-surface-variant" />
                <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                  <Link
                    to={n.link || "/notifications"}
                    onClick={() => !n.read_at && read.mutate(n.id)}
                    className={"text-body-md text-on-surface hover:underline " + (n.read_at ? "" : "font-semibold")}
                  >
                    {n.title}
                  </Link>
                  {n.body && <p className="text-body-sm text-on-surface-variant">{n.body}</p>}
                  <p className="font-mono text-label-sm text-muted">{relativeTime(n.created_at)}</p>
                </div>
                {!n.read_at && (
                  <button
                    type="button"
                    onClick={() => read.mutate(n.id)}
                    className="self-start font-mono text-label-sm text-on-surface-variant underline"
                    aria-label={`Mark "${n.title}" as read`}
                  >
                    Mark read
                  </button>
                )}
              </li>
            ))}
          </ul>
        </Panel>
      )}
    </div>
  );
}
