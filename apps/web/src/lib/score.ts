import type { TagTone } from "../components/ui/Tag";

/**
 * Match score → tone mapping (DESIGN.md functional mapping):
 * ≥90 citron, 70–89 olive, <70 terracotta.
 */
export function matchTone(score: number): TagTone {
  if (score >= 90) return "citron";
  if (score >= 70) return "olive";
  return "terracotta";
}
