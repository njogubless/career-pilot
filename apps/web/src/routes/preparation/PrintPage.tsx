import { Link, useParams } from "react-router";
import { Button } from "../../components/ui/Button";
import { Loading } from "../../components/ui/Loading";
import { useApplicationDocument, type Content } from "../../lib/preparation";

/** Single-column print layout; the browser's "Save as PDF" produces the PDF (ADR-021). */
function PrintBody({ content }: { content: Content }) {
  const name = content.sections.flatMap((s) => s.blocks).find((b) => b.type === "contact")?.text ?? "";
  const headings = content.kind !== "COVER_LETTER";
  return (
    <article className="mx-auto max-w-[720px] font-sans text-[11pt] leading-snug text-black">
      {content.sections.map((s) => (
        <section key={s.key} className="mb-4 break-inside-avoid-page">
          {headings && s.key !== "contact" && s.blocks.length > 0 && (
            <h2 className="mb-1 border-b border-neutral-400 pb-0.5 text-[10.5pt] font-bold uppercase tracking-wide">{s.title}</h2>
          )}
          {s.blocks.map((b) => {
            switch (b.type) {
              case "contact":
                return (
                  <header key={b.id} className="mb-2">
                    <h1 className="text-[20pt] font-bold">{b.text}</h1>
                    <p className="text-neutral-600">{[b.meta.email, b.meta.location, b.meta.links].filter(Boolean).join(" · ")}</p>
                  </header>
                );
              case "entry":
                return (
                  <p key={b.id} className="mt-2 flex justify-between gap-4 font-bold">
                    <span>{b.text}</span>
                    <span className="font-normal text-neutral-600">{[b.meta.location, b.meta.dates].filter(Boolean).join(" · ")}</span>
                  </p>
                );
              case "bullet":
                return (
                  <p key={b.id} className="pl-4 -indent-3">
                    • {b.text}
                  </p>
                );
              case "answer":
                return b.text ? (
                  <p key={b.id} className="whitespace-pre-wrap">
                    {b.text}
                  </p>
                ) : (
                  <p key={b.id} className="italic text-[#A0522D]">
                    [Answer needed: {b.meta.question_for_user}]
                  </p>
                );
              default:
                return (
                  <p key={b.id} className="mb-2 whitespace-pre-wrap">
                    {b.text}
                    {b.meta.role === "closing" && name && (
                      <>
                        <br />
                        {name}
                      </>
                    )}
                  </p>
                );
            }
          })}
        </section>
      ))}
    </article>
  );
}

export function PrintPage() {
  const { id = "" } = useParams();
  const doc = useApplicationDocument(id);
  if (doc.isPending) return <Loading label="Preparing print view" />;
  if (doc.isError || !doc.data.current_version) {
    return <p className="p-space-lg text-terracotta">{doc.error?.message ?? "This document has no content yet."}</p>;
  }
  const v = doc.data.current_version;
  return (
    <div className="min-h-screen bg-white p-8 print:p-0">
      <div className="mx-auto mb-6 flex max-w-[720px] items-center justify-between gap-4 print:hidden">
        <p className="text-body-sm text-neutral-600">
          Version {v.version_no}
          {doc.data.approval_state === "CURRENT" ? " (approved)" : " (not approved)"}. Use your browser's print dialog and choose “Save as PDF”.
        </p>
        <div className="flex gap-2">
          <Link to={`/prepare/${id}`} className="px-3 py-2 text-[14px] underline">
            Back
          </Link>
          <Button icon="print" onClick={() => window.print()}>
            Print
          </Button>
        </div>
      </div>
      <PrintBody content={v.content} />
    </div>
  );
}
