import { useState } from "react";
import { Button } from "../../components/ui/Button";
import { Panel } from "../../components/ui/Panel";
import { useContactActions, useContacts, type Contact } from "../../lib/outreach";
import { ContactForm } from "./ContactForm";
import { DraftDialog } from "./DraftDialog";
import { ContactRow } from "./OutreachPage";

/** Contacts for one job: addresses the posting publishes, plus your own. */
export function JobContactsPanel({ jobId }: { jobId: string }) {
  const contacts = useContacts(jobId);
  const { discover } = useContactActions();
  const [editing, setEditing] = useState<Contact | "new" | null>(null);
  const [drafting, setDrafting] = useState<Contact | null>(null);
  const list = contacts.data?.data ?? [];
  return (
    <Panel>
      <div className="border-b border-stone p-space-md">
        <p className="label-caps text-outline">Recruiter outreach</p>
        <h2 className="font-serif text-headline-sm text-on-surface">Contacts</h2>
      </div>
      <div className="flex flex-wrap gap-space-sm p-space-md">
        <Button variant="secondary" icon="search" className="py-1 text-[13px]" disabled={discover.isPending} onClick={() => discover.mutate(jobId)}>
          Find contacts in the posting
        </Button>
        <Button variant="ghost" icon="person_add" className="py-1 text-[13px]" onClick={() => setEditing("new")}>
          Add contact
        </Button>
      </div>
      {discover.data && (
        <p role="status" className="px-space-md pb-space-sm text-body-sm text-on-surface-variant">
          {discover.data.data.length === 0
            ? "This posting does not publish a recruiting email address."
            : `${discover.data.data.filter((d) => d.created).length} new address(es) found in the posting.`}
        </p>
      )}
      {discover.isError && <p className="px-space-md pb-space-sm text-body-sm text-terracotta">{discover.error.message}</p>}
      {list.length > 0 && (
        <ul className="divide-y divide-stone border-t border-stone">
          {list.map((c) => (
            <ContactRow key={c.id} c={c} onEdit={() => setEditing(c)} onDraft={() => setDrafting(c)} />
          ))}
        </ul>
      )}
      {editing && <ContactForm jobId={jobId} contact={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
      {drafting && <DraftDialog contact={drafting} jobId={jobId} onClose={() => setDrafting(null)} />}
    </Panel>
  );
}
