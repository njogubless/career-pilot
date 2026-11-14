import { describe, expect, it } from "vitest";
import type { Proposal } from "../lib/documents";
import { buildImport, initChoices } from "../lib/extractionImport";
import type { Basics } from "../lib/profile";

const proposal: Proposal = {
  personal: {
    full_name: "Amani",
    headline: "Backend Engineer",
    summary: null,
    location_city: null,
    location_country: "KE",
    links: [
      { label: "GitHub", url: "https://github.com/a" },
      { label: "Bad", url: "javascript:alert(1)" },
    ],
    found: {},
  },
  experiences: [
    { employer: "Acme", title: "Engineer", location: null, start_date: "2021-03", end_date: null, is_current: true, description: null, achievements: ["Built X"], evidence: "Engineer, Acme", evidence_found: true },
    { employer: "Ghost", title: "CTO", location: null, start_date: "2019", end_date: "2020", is_current: false, description: null, achievements: [], evidence: "CTO Ghost", evidence_found: false },
  ],
  education: [],
  skills: [{ name: "Go", category: null, evidence: "Go", evidence_found: true }],
  certifications: [],
  projects: [],
  warnings: [],
};

const current: Basics = { headline: "Old", summary: "Kept summary", location_city: "Nairobi", location_country: null, years_experience: 3, links: [] };

describe("extraction import", () => {
  it("pre-selects only items whose evidence was found, never pre-verifies", () => {
    const c = initChoices(proposal);
    expect(c.experiences.map((x) => x.accept)).toEqual([true, false]);
    expect(c.experiences.every((x) => !x.verified)).toBe(true);
  });

  it("maps accepted items to the API shape and preserves existing basics", () => {
    const c = initChoices(proposal);
    c.skills[0].verified = true;
    const body = buildImport(proposal, c, true, current);
    expect(body.experiences).toEqual([
      { item: { employer: "Acme", title: "Engineer", location: "", start_date: "2021-03-01", end_date: null, is_current: true, description: "", achievements: ["Built X"] }, verified: false },
    ]);
    expect(body.skills).toEqual([{ item: { name: "Go", category: "" }, verified: true }]);
    expect(body.basics).toMatchObject({ headline: "Backend Engineer", summary: "Kept summary", location_city: "Nairobi", location_country: "KE" });
    expect(body.basics?.links).toEqual([{ label: "GitHub", url: "https://github.com/a" }]);
  });

  it("omits basics when the user opts out", () => {
    expect(buildImport(proposal, initChoices(proposal), false, current).basics).toBeUndefined();
  });
});
