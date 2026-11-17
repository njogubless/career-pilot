import { useState } from "react";
import { Button } from "../../components/ui/Button";
import { TextArea, TextInput } from "../../components/ui/Form";
import { Modal } from "../../components/ui/Modal";
import { apiFieldError } from "../../lib/api";
import { useContactActions, type Contact, type ContactInput } from "../../lib/outreach";

/** Add or edit a contact. Addresses are entered by the user, never suggested. */
export function ContactForm({ contact, jobId, onClose }: { contact?: Contact; jobId?: string; onClose: () => void }) {
  const { create, update } = useContactActions();
  const [v, setV] = useState<ContactInput>({
    name: contact?.name ?? "",
    title: contact?.title ?? "",
    company: contact?.company ?? "",
    profile_url: contact?.profile_url ?? "",
    email: contact?.email ?? "",
    notes: contact?.notes ?? "",
    do_not_contact: contact?.do_not_contact ?? false,
    do_not_contact_reason: contact?.do_not_contact_reason ?? "",
    job_id: contact?.job_id ?? jobId ?? null,
  });
  const m = contact ? update : create;
  const set = (k: keyof ContactInput) => (e: { target: { value: string } }) => setV({ ...v, [k]: e.target.value });
  const save = () => (contact ? update.mutate({ id: contact.id, body: v }, { onSuccess: onClose }) : create.mutate(v, { onSuccess: onClose }));
  const err = (f: string) => apiFieldError(m.error, f);
  return (
    <Modal
      title={contact ? "Edit contact" : "Add a contact"}
      onClose={onClose}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={m.isPending} onClick={save}>
            Save contact
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-space-sm">
        <p className="text-body-sm text-on-surface-variant">
          Only add addresses you know are correct, for example from the posting, the company's site or the person themselves. CareerPilot never guesses
          addresses; it only checks that the domain accepts mail.
        </p>
        <TextInput label="Name" value={v.name} onChange={set("name")} error={err("name")} maxLength={200} />
        <TextInput label="Job title" value={v.title} onChange={set("title")} maxLength={200} />
        <TextInput label="Company" value={v.company} onChange={set("company")} maxLength={200} />
        <TextInput label="Email" type="email" value={v.email} onChange={set("email")} error={err("email")} maxLength={320} />
        <TextInput label="Public profile URL" value={v.profile_url} onChange={set("profile_url")} error={err("profile_url")} maxLength={500} />
        <TextArea label="Notes" rows={2} value={v.notes} onChange={set("notes")} maxLength={2000} />
        <label className="flex items-start gap-space-xs text-body-md text-on-surface">
          <input type="checkbox" checked={v.do_not_contact} onChange={(e) => setV({ ...v, do_not_contact: e.target.checked })} className="mt-1" />
          Do not contact. Blocks sending and cancels follow-ups to this person.
        </label>
        {v.do_not_contact && <TextInput label="Reason (optional)" value={v.do_not_contact_reason} onChange={set("do_not_contact_reason")} maxLength={500} />}
        {m.isError && !err("email") && !err("name") && <p role="alert" className="text-body-sm text-terracotta">{m.error.message}</p>}
      </div>
    </Modal>
  );
}
