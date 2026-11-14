/** Builds the import payload from a reviewed CV extraction. */
import { toApiDate, type Proposal } from "./documents";
import type { Basics } from "./profile";

export type Choice = { accept: boolean; verified: boolean };
export type Section = "experiences" | "education" | "skills" | "certifications" | "projects";

export function initChoices(p: Proposal): Record<Section, Choice[]> {
  // Items whose evidence was not found in the CV start unticked.
  const mk = (items: { evidence_found: boolean }[]) => items.map((i) => ({ accept: i.evidence_found, verified: false }));
  return {
    experiences: mk(p.experiences),
    education: mk(p.education),
    skills: mk(p.skills),
    certifications: mk(p.certifications),
    projects: mk(p.projects),
  };
}

export function buildImport(p: Proposal, c: Record<Section, Choice[]>, applyBasics: boolean, current: Basics) {
  const pick = <T, U>(items: T[], section: Section, map: (t: T) => U) =>
    items.flatMap((it, i) => (c[section][i]?.accept ? [{ item: map(it), verified: c[section][i].verified }] : []));
  const knownUrls = new Set(current.links.map((l) => l.url));
  return {
    basics: applyBasics
      ? {
          headline: p.personal.headline ?? current.headline,
          summary: p.personal.summary ?? current.summary,
          location_city: p.personal.location_city ?? current.location_city,
          location_country: p.personal.location_country ?? current.location_country,
          years_experience: current.years_experience,
          links: [...current.links, ...p.personal.links.filter((l) => /^https?:\/\//i.test(l.url) && !knownUrls.has(l.url))].slice(0, 20),
        }
      : undefined,
    experiences: pick(p.experiences, "experiences", (e) => ({
      employer: e.employer,
      title: e.title,
      location: e.location ?? "",
      start_date: toApiDate(e.start_date),
      end_date: e.is_current ? null : toApiDate(e.end_date),
      is_current: e.is_current,
      description: e.description ?? "",
      achievements: e.achievements,
    })),
    education: pick(p.education, "education", (e) => ({
      institution: e.institution,
      degree: e.degree ?? "",
      field: e.field ?? "",
      start_date: toApiDate(e.start_date),
      end_date: toApiDate(e.end_date),
    })),
    skills: pick(p.skills, "skills", (s) => ({ name: s.name, category: s.category ?? "" })),
    certifications: pick(p.certifications, "certifications", (cert) => ({
      name: cert.name,
      issuer: cert.issuer ?? "",
      issued_date: toApiDate(cert.issued_date),
      credential_url: "",
    })),
    projects: pick(p.projects, "projects", (pr) => ({
      name: pr.name,
      description: pr.description ?? "",
      url: pr.url && /^https?:\/\//i.test(pr.url) ? pr.url : "",
    })),
  };
}
