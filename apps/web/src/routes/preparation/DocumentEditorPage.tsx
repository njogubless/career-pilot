import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { Button } from "../../components/ui/Button";
import { Icon } from "../../components/ui/Icon";
import { Loading } from "../../components/ui/Loading";
import { Panel } from "../../components/ui/Panel";
import { Tag } from "../../components/ui/Tag";
import { ApiError } from "../../lib/api";
import {
  exportUrl,
  kindLabel,
  originLabel,
  statusLabel,
  statusTone,
  useApplicationDocument,
  useDeleteDocument,
  useSaveContent,
  useVersion,
  type Content,
  type DocumentView,
  type Issue,
  type Validation,
} from "../../lib/preparation";
import { BlockEditor } from "./BlockEditor";
import { ApprovalPanel, FactCheckPanel, HistoryPanel, QuestionsPanel } from "./SidePanels";

function issueMap(v: Validation | null | undefined) {
  const m = new Map<string, Issue[]>();
  for (const i of v?.issues ?? []) m.set(i.block_id, [...(m.get(i.block_id) ?? []), i]);
  return m;
}

function Breadcrumb({ doc }: { doc: DocumentView }) {
  return (
    <nav aria-label="Breadcrumb" className="font-mono text-label-md text-muted">
      <Link to="/discover" className="hover:underline">
        Discover jobs
      </Link>{" "}
      /{" "}
      <Link to={`/jobs/${doc.job_id}`} className="hover:underline">
        {doc.company} · {doc.job_title}
      </Link>{" "}
      / <span className="text-on-surface">{kindLabel[doc.kind]}</span>
    </nav>
  );
}

function GenerationBanner({ doc }: { doc: DocumentView }) {
  const g = doc.latest_generation;
  if (!g) return null;
  if (g.status === "QUEUED" || g.status === "RUNNING") {
    return (
      <div role="status" className="flex items-center gap-space-sm rounded-sm border border-stone bg-surface-container px-space-md py-space-sm text-body-md">
        <Icon name="progress_activity" size={18} className="animate-spin text-olive" />
        {doc.current_version ? "A new draft is being generated. It will become a new version; your current version is kept." : "Generating your draft from verified profile facts…"}
      </div>
    );
  }
  if (g.status === "FAILED" && g.version_id === null && (!doc.current_version || new Date(g.updated_at) > new Date(doc.current_version.created_at))) {
    return (
      <div role="alert" className="rounded-sm border border-terracotta px-space-md py-space-sm text-body-md text-on-surface">
        <span className="font-semibold text-terracotta">Generation failed.</span> {g.error_message ?? "The draft could not be generated."}
        {g.error_code === "PROFILE_INCOMPLETE" && (
          <>
            {" "}
            <Link to="/profile" className="font-semibold underline">
              Open your profile
            </Link>
          </>
        )}
      </div>
    );
  }
  return null;
}

export function DocumentEditorPage() {
  const { id = "" } = useParams();
  const doc = useApplicationDocument(id);

  if (doc.isPending) return <Loading label="Loading document" />;
  if (doc.isError) {
    return (
      <Panel className="p-space-lg">
        <p role="alert" className="text-body-md text-terracotta">
          {doc.error instanceof ApiError && doc.error.status === 404 ? "This document does not exist." : doc.error.message}
        </p>
        <Link to="/documents" className="mt-space-sm inline-block underline">
          Back to documents
        </Link>
      </Panel>
    );
  }
  const d = doc.data;
  if (!d.current_version) {
    return (
      <div className="flex flex-col gap-space-lg">
        <Breadcrumb doc={d} />
        <h1 className="font-serif text-headline-lg text-on-surface">{kindLabel[d.kind]}</h1>
        <GenerationBanner doc={d} />
        <Link to={`/jobs/${d.job_id}`} className="self-start underline">
          Back to the job
        </Link>
      </div>
    );
  }
  return <Workspace key={d.current_version.id} doc={d} />;
}

function Workspace({ doc }: { doc: DocumentView }) {
  const version = doc.current_version!;
  const navigate = useNavigate();
  const [draft, setDraft] = useState<Content>(version.content);
  const [live, setLive] = useState<Validation | null>(null);
  const [viewing, setViewing] = useState<string | null>(null);
  const save = useSaveContent(doc.id);
  const del = useDeleteDocument(doc.id);
  const viewed = useVersion(doc.id, viewing);
  const dirty = useMemo(() => JSON.stringify(draft) !== JSON.stringify(version.content), [draft, version.content]);
  const validation = live ?? version.validation;
  const issues = useMemo(() => issueMap(validation), [validation]);
  const facts = useMemo(() => new Map(doc.facts.map((f) => [f.ref, f])), [doc.facts]);

  useEffect(() => {
    if (!dirty) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);

  const status = validation?.status ?? "NOT_VALIDATED";
  const conflict = save.error instanceof ApiError && save.error.status === 409;

  return (
    <div className="flex flex-col gap-space-lg">
      <Breadcrumb doc={doc} />
      <header className="flex flex-col gap-space-sm lg:flex-row lg:items-end lg:justify-between">
        <div className="flex flex-col gap-space-xs">
          <p className="label-caps text-outline">Application preparation</p>
          <h1 className="font-serif text-headline-lg-mobile text-on-surface md:text-headline-lg">{kindLabel[doc.kind]}</h1>
          <div className="flex flex-wrap items-center gap-1">
            <Tag tone="stone">
              v{version.version_no} · {originLabel[version.origin]}
            </Tag>
            <Tag tone={statusTone[status]}>{statusLabel[status]}</Tag>
            {doc.approval_state === "CURRENT" && <Tag tone="olive">Approved</Tag>}
            {dirty && <Tag tone="terracotta">Unsaved changes</Tag>}
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-space-sm">
          {dirty && (
            <Button variant="ghost" onClick={() => setDraft(version.content)}>
              Discard changes
            </Button>
          )}
          <Button icon="save" iconClassName="text-secondary-fixed" disabled={!dirty || save.isPending} onClick={() => save.mutate({ base_version_id: version.id, content: draft })}>
            {save.isPending ? "Saving…" : "Save as new version"}
          </Button>
        </div>
      </header>

      <GenerationBanner doc={doc} />
      {save.isError && (
        <div role="alert" className="rounded-sm border border-terracotta px-space-md py-space-sm text-body-md">
          <span className="text-terracotta">{save.error.message}</span>
          {conflict && <span className="text-on-surface-variant"> Copy any text you want to keep, then reload the page.</span>}
        </div>
      )}

      <div className="grid grid-cols-1 items-start gap-space-lg lg:grid-cols-12">
        <div className="flex min-w-0 flex-col gap-space-lg lg:col-span-8">
          {viewing && viewed.data && (
            <Panel className="border-graphite/40">
              <div className="flex items-center justify-between border-b border-stone px-space-md py-space-sm">
                <p className="text-body-md text-on-surface">
                  Viewing version {viewed.data.version_no} ({originLabel[viewed.data.origin]}), read-only
                </p>
                <Button variant="ghost" className="py-1" onClick={() => setViewing(null)}>
                  Close
                </Button>
              </div>
              <BlockEditor content={viewed.data.content} onChange={() => undefined} issuesByBlock={issueMap(viewed.data.validation)} facts={facts} readOnly />
            </Panel>
          )}
          <Panel>
            <BlockEditor content={draft} onChange={setDraft} issuesByBlock={issues} facts={facts} />
          </Panel>
          <Panel className="flex flex-wrap items-center gap-space-sm p-space-md">
            <span className="label-caps text-outline">Export v{version.version_no}</span>
            {(["docx", "txt", "md"] as const).map((f) => (
              <a
                key={f}
                href={exportUrl(doc.id, version.id, f)}
                download
                className="inline-flex items-center gap-1 rounded-sm border border-stone px-space-sm py-1 font-mono text-label-md text-on-surface hover:border-graphite"
              >
                <Icon name="download" size={14} /> {f.toUpperCase()}
              </a>
            ))}
            <Link
              to={`/prepare/${doc.id}/print`}
              target="_blank"
              className="inline-flex items-center gap-1 rounded-sm border border-stone px-space-sm py-1 font-mono text-label-md text-on-surface hover:border-graphite"
            >
              <Icon name="print" size={14} /> PRINT / PDF
            </Link>
            {dirty && <span className="text-body-sm text-muted">Exports use the saved version.</span>}
          </Panel>
        </div>
        <aside className="flex min-w-0 flex-col gap-space-lg lg:col-span-4">
          <FactCheckPanel docId={doc.id} version={version} validation={validation} live={live !== null} onLive={setLive} dirty={dirty} />
          <QuestionsPanel version={version} />
          <ApprovalPanel doc={doc} version={version} validation={validation} dirty={dirty} />
          <HistoryPanel docId={doc.id} currentId={version.id} approvedId={doc.approved_version_id} viewing={viewing} onView={setViewing} dirty={dirty} />
          <Panel className="flex flex-col gap-space-xs p-space-md">
            <p className="label-caps text-outline">Danger zone</p>
            {del.isError && <p className="text-body-sm text-terracotta">{del.error.message}</p>}
            <Button
              variant="destructive"
              className="self-start"
              disabled={del.isPending}
              onClick={() => {
                if (window.confirm("Delete this document and all of its versions? This cannot be undone.")) {
                  del.mutate(undefined, { onSuccess: () => navigate(`/jobs/${doc.job_id}`) });
                }
              }}
            >
              Delete document
            </Button>
          </Panel>
        </aside>
      </div>
    </div>
  );
}
