import clsx from "clsx";
import { useState } from "react";
import { Link, useSearchParams } from "react-router";
import { Button } from "../../components/ui/Button";
import { Icon } from "../../components/ui/Icon";
import { Loading } from "../../components/ui/Loading";
import { EmptyState, Panel } from "../../components/ui/Panel";
import { Tag } from "../../components/ui/Tag";
import { relativeTime } from "../../lib/format";
import {
  messageStatusLabel,
  messageStatusTone,
  useContactActions,
  useContacts,
  useGmailStatus,
  useMessages,
  usePollReplies,
  verificationLabel,
  verificationTone,
  type Contact,
} from "../../lib/outreach";
import { ContactForm } from "./ContactForm";
import { DraftDialog } from "./DraftDialog";

export function GmailBanner() {
  const gmail = useGmailStatus();
  const g = gmail.data;
  if (!g) return null;
  if (!g.configured) {
    return (
      <p className="border-l-2 border-terracotta pl-space-sm text-body-sm text-on-surface">
        Gmail is not configured on this server, so emails cannot be sent yet. You can still manage contacts and prepare drafts.
      </p>
    );
  }
  if (!g.account) {
    return (
      <p className="border-l-2 border-citron pl-space-sm text-body-sm text-on-surface">
        Connect your Gmail account in{" "}
        <Link to="/settings#gmail" className="font-semibold underline">
          Settings
        </Link>{" "}
        to send approved emails from your own address.
      </p>
    );
  }
  if (g.account.status !== "CONNECTED") {
    return (
      <p className="border-l-2 border-terracotta pl-space-sm text-body-sm text-on-surface">
        Gmail access for {g.account.email} has expired.{" "}
        <Link to="/settings#gmail" className="font-semibold underline">
          Reconnect
        </Link>{" "}
        to send emails and detect replies.
      </p>
    );
  }
  return <p className="font-mono text-label-md text-on-surface-variant">Sending from {g.account.email}</p>;
}

function Messages() {
  const [status, setStatus] = useState("");
  const msgs = useMessages({ status });
  const statuses = ["", "DRAFT", "APPROVED", "SENT", "FAILED", "UNKNOWN"];
  return (
    <Panel>
      <div className="flex flex-wrap gap-1 border-b border-stone p-space-sm">
        {statuses.map((s) => (
          <button
            key={s || "all"}
            onClick={() => setStatus(s)}
            className={clsx("rounded-xs px-space-sm py-1 font-mono text-label-md", status === s ? "bg-surface-container text-on-surface" : "text-on-surface-variant")}
          >
            {s ? messageStatusLabel[s as keyof typeof messageStatusLabel] : "All"}
          </button>
        ))}
      </div>
      {msgs.isPending && <Loading />}
      {msgs.data?.data.length === 0 && (
        <EmptyState icon="mail" title="No emails here">
          Draft an email from a contact. Every email is reviewed and approved by you before it is sent from your own Gmail.
        </EmptyState>
      )}
      <ul className="divide-y divide-stone">
        {msgs.data?.data.map((m) => (
          <li key={m.id}>
            <Link to={`/outreach/messages/${m.id}`} className="flex flex-wrap items-center justify-between gap-space-sm px-space-md py-space-sm hover:bg-surface-container-low">
              <div className="flex min-w-0 flex-col">
                <span className="truncate font-semibold text-on-surface">{m.subject}</span>
                <span className="truncate text-body-sm text-on-surface-variant">
                  {m.kind === "FOLLOW_UP" ? "Follow-up · " : ""}
                  {m.contact_name || m.to_email} · {m.to_email}
                </span>
              </div>
              <div className="flex items-center gap-space-sm">
                {m.replied_at && <Tag tone="citron">Replied</Tag>}
                <Tag tone={messageStatusTone[m.status]}>{messageStatusLabel[m.status]}</Tag>
                <span className="font-mono text-label-sm text-muted">{relativeTime(m.sent_at ?? m.updated_at)}</span>
              </div>
            </Link>
          </li>
        ))}
      </ul>
    </Panel>
  );
}

export function ContactRow({ c, onEdit, onDraft }: { c: Contact; onEdit: () => void; onDraft: () => void }) {
  const { verify } = useContactActions();
  return (
    <li className="flex flex-col gap-1 px-space-md py-space-sm">
      <div className="flex flex-wrap items-center justify-between gap-space-sm">
        <div className="flex min-w-0 flex-col">
          <span className="font-semibold text-on-surface">{c.name || (c.company ? `Contact at ${c.company}` : "Unnamed contact")}</span>
          <span className="text-body-sm text-on-surface-variant">{[c.title, c.company].filter(Boolean).join(" · ") || "Role not recorded"}</span>
        </div>
        <div className="flex flex-wrap items-center gap-1">
          {c.do_not_contact && <Tag tone="terracotta">Do not contact</Tag>}
          {c.email ? <Tag tone={verificationTone[c.email_verification]}>{verificationLabel[c.email_verification]}</Tag> : <Tag tone="outline">No email</Tag>}
          <Tag tone="outline">{c.source === "JOB_POSTING" ? "From the posting" : "Added by you"}</Tag>
        </div>
      </div>
      {c.email && <span className="font-mono text-label-md text-on-surface">{c.email}</span>}
      {c.source === "JOB_POSTING" && c.source_evidence && <p className="text-body-sm italic text-muted">“{c.source_evidence}”</p>}
      <div className="flex flex-wrap items-center gap-space-sm">
        <Button variant="secondary" className="px-space-sm py-1 text-[13px]" disabled={!c.email || c.do_not_contact} onClick={onDraft}>
          Draft email
        </Button>
        <Button variant="ghost" className="px-space-sm py-1 text-[13px]" onClick={onEdit}>
          Edit
        </Button>
        {c.email && c.email_verification !== "BOUNCED" && (
          <Button variant="ghost" className="px-space-sm py-1 text-[13px]" disabled={verify.isPending} onClick={() => verify.mutate(c.id)}>
            Re-check domain
          </Button>
        )}
        <span className="font-mono text-label-sm text-muted">
          {c.messages_sent ? `${c.messages_sent} sent · last ${relativeTime(c.last_contacted_at)}` : "Not contacted"}
        </span>
      </div>
    </li>
  );
}

function Contacts() {
  const contacts = useContacts();
  const [editing, setEditing] = useState<Contact | "new" | null>(null);
  const [drafting, setDrafting] = useState<Contact | null>(null);
  return (
    <Panel>
      <div className="flex items-center justify-between border-b border-stone p-space-md">
        <p className="text-body-sm text-on-surface-variant">Contacts come from postings that publish an address, or from you. None are guessed or bought.</p>
        <Button icon="person_add" className="shrink-0" onClick={() => setEditing("new")}>
          Add contact
        </Button>
      </div>
      {contacts.isPending && <Loading />}
      {contacts.data?.data.length === 0 && (
        <EmptyState icon="contacts" title="No contacts yet">
          Add a recruiter you know, or use “Find contacts in the posting” on a job page.
        </EmptyState>
      )}
      <ul className="divide-y divide-stone">
        {contacts.data?.data.map((c) => (
          <ContactRow key={c.id} c={c} onEdit={() => setEditing(c)} onDraft={() => setDrafting(c)} />
        ))}
      </ul>
      {editing && <ContactForm contact={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
      {drafting && <DraftDialog contact={drafting} onClose={() => setDrafting(null)} />}
    </Panel>
  );
}

export function OutreachPage() {
  const [params, setParams] = useSearchParams();
  const tab = params.get("tab") === "contacts" ? "contacts" : "messages";
  const poll = usePollReplies();
  const gmail = useGmailStatus();
  return (
    <div className="flex flex-col gap-space-lg">
      <header className="flex flex-col gap-space-md lg:flex-row lg:items-end lg:justify-between">
        <div className="flex max-w-2xl flex-col gap-space-xs">
          <p className="label-caps text-outline">Recruiter outreach</p>
          <h1 className="font-serif text-headline-lg-mobile text-on-surface md:text-headline-lg">Recruiter Outreach</h1>
          <p className="text-body-lg text-on-surface-variant">
            Personal emails from your own Gmail, built from your verified profile. Each email is approved by you, sending limits and cooldowns apply,
            and replies cancel follow-ups.
          </p>
          <GmailBanner />
        </div>
        <div className="flex flex-col items-start gap-1">
          <Button variant="secondary" icon="mark_email_unread" disabled={poll.isPending || gmail.data?.account?.status !== "CONNECTED"} onClick={() => poll.mutate()}>
            Check for replies
          </Button>
          {poll.data && (
            <span role="status" className="text-body-sm text-on-surface-variant">
              {poll.data.threads_checked} thread(s) checked · {poll.data.replies_found} new repl{poll.data.replies_found === 1 ? "y" : "ies"}
              {poll.data.bounces_found ? ` · ${poll.data.bounces_found} bounce(s)` : ""}
            </span>
          )}
          {poll.isError && <span className="text-body-sm text-terracotta">{poll.error.message}</span>}
        </div>
      </header>
      <div role="tablist" className="flex gap-space-sm border-b border-stone">
        {(["messages", "contacts"] as const).map((t) => (
          <button
            key={t}
            role="tab"
            aria-selected={tab === t}
            onClick={() => setParams(t === "messages" ? {} : { tab: t })}
            className={clsx("-mb-px border-b-2 px-space-sm py-space-xs text-body-md", tab === t ? "border-graphite text-on-surface" : "border-transparent text-on-surface-variant")}
          >
            <Icon name={t === "messages" ? "mail" : "contacts"} size={16} className="mr-1 align-[-3px]" />
            {t === "messages" ? "Emails" : "Contacts"}
          </button>
        ))}
      </div>
      {tab === "messages" ? <Messages /> : <Contacts />}
    </div>
  );
}
