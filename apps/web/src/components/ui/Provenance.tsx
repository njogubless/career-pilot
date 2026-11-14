import type { Source, Verification } from "../../lib/profile";
import { Tag } from "./Tag";

const sourceLabel: Record<Source, string> = {
  USER_ENTERED: "Entered by you",
  CV_IMPORT: "From CV",
  AI_INFERRED: "AI inferred",
};

/** Shows where a fact came from and whether the user has confirmed it (MASTER_BUILD_PROMPT §5). */
export function ProvenanceBadge({ source, verification }: { source: Source; verification: Verification }) {
  return (
    <span className="inline-flex flex-wrap items-center gap-1">
      {verification === "VERIFIED" && <Tag tone="olive">Verified</Tag>}
      {verification === "UNVERIFIED" && <Tag tone="terracotta">Needs review</Tag>}
      {verification === "REJECTED" && <Tag tone="outline">Rejected</Tag>}
      {source !== "USER_ENTERED" && <Tag tone="outline">{sourceLabel[source]}</Tag>}
    </span>
  );
}
