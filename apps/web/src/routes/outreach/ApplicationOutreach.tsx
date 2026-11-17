import { Link } from "react-router";
import { Panel } from "../../components/ui/Panel";
import { Tag } from "../../components/ui/Tag";
import { relativeTime } from "../../lib/format";
import { messageStatusLabel, messageStatusTone, useMessages } from "../../lib/outreach";

/** Emails and replies associated with an application. */
export function ApplicationOutreach({ applicationId, jobId }: { applicationId: string; jobId: string }) {
  const msgs = useMessages({ application_id: applicationId });
  const list = msgs.data?.data ?? [];
  return (
    <Panel>
      <div className="flex items-center justify-between border-b border-stone p-space-md">
        <div>
          <p className="label-caps text-outline">Recruiter interactions</p>
          <h2 className="font-serif text-headline-sm text-on-surface">Outreach</h2>
        </div>
        <Link to={`/jobs/${jobId}`} className="font-mono text-label-md underline">
          Contacts for this job
        </Link>
      </div>
      {list.length === 0 ? (
        <p className="p-space-md text-body-sm text-muted">No emails linked to this application yet.</p>
      ) : (
        <ul className="divide-y divide-stone">
          {list.map((m) => (
            <li key={m.id}>
              <Link to={`/outreach/messages/${m.id}`} className="flex flex-wrap items-center justify-between gap-space-sm px-space-md py-space-sm hover:bg-surface-container-low">
                <span className="text-body-md text-on-surface">
                  {m.kind === "FOLLOW_UP" ? "Follow-up to " : ""}
                  {m.contact_name || m.to_email}
                </span>
                <span className="flex items-center gap-space-sm">
                  {m.replied_at && <Tag tone="citron">Replied {relativeTime(m.replied_at)}</Tag>}
                  <Tag tone={messageStatusTone[m.status]}>{messageStatusLabel[m.status]}</Tag>
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
}
