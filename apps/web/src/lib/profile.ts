import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";

export type Source = "USER_ENTERED" | "CV_IMPORT" | "AI_INFERRED";
export type Verification = "VERIFIED" | "UNVERIFIED" | "REJECTED";
type Prov = { source: Source; verification: Verification };

export type Link = { label: string; url: string };
export type Basics = {
  headline: string;
  summary: string;
  location_city: string;
  location_country: string | null;
  years_experience: number | null;
  links: Link[];
};
export type Experience = Prov & {
  id: string;
  employer: string;
  title: string;
  location: string;
  start_date: string | null;
  end_date: string | null;
  is_current: boolean;
  description: string;
  achievements: string[];
};
export type Education = Prov & {
  id: string;
  institution: string;
  degree: string;
  field: string;
  start_date: string | null;
  end_date: string | null;
};
export type Skill = Prov & { id: string; name: string; category: string; level: string | null; years: number | null };
export type Certification = Prov & {
  id: string;
  name: string;
  issuer: string;
  issued_date: string | null;
  expires_date: string | null;
  credential_url: string;
};
export type Project = Prov & { id: string; name: string; description: string; url: string };
export type Preferences = {
  target_roles: string[];
  industries: string[];
  employment_types: string[];
  preferred_locations: string[];
  remote_mode: "REMOTE_ONLY" | "HYBRID_OK" | "ONSITE_OK";
  work_eligible_countries: string[];
  min_salary: number | null;
  salary_currency: string | null;
  salary_period: "YEAR" | "MONTH" | "HOUR" | null;
  needs_sponsorship: boolean | null;
  willing_to_relocate: boolean | null;
  notice_period_days: number | null;
  seniority_levels: string[];
};
export type MissingItem = { key: string; label: string; impact: string; weight: number };
export type Completeness = { score: number; missing: MissingItem[]; unverified_facts: number };
export type Profile = {
  basics: Basics;
  experiences: Experience[];
  education: Education[];
  skills: Skill[];
  certifications: Certification[];
  projects: Project[];
  preferences: Preferences;
  completeness: Completeness;
};

export type FactKind = "experiences" | "education" | "skills" | "certifications" | "projects";

export const profileKey = ["profile"] as const;

export function useProfile() {
  return useQuery({ queryKey: profileKey, queryFn: () => api.get<Profile>("/api/v1/profiles/me") });
}

/** Wraps a mutation that changes the profile and refreshes it afterwards. */
export function useProfileMutation<I, O = unknown>(fn: (input: I) => Promise<O>) {
  const qc = useQueryClient();
  return useMutation({ mutationFn: fn, onSuccess: () => qc.invalidateQueries({ queryKey: profileKey }) });
}

export const profileApi = {
  putBasics: (b: Basics) => api.put<Basics>("/api/v1/profiles/me/basics", b),
  putPreferences: (p: Preferences) => api.put<Preferences>("/api/v1/profiles/me/preferences", p),
  create: (kind: FactKind, body: object) => api.post(`/api/v1/profiles/me/${kind}`, body),
  update: (kind: FactKind, id: string, body: object) => api.put(`/api/v1/profiles/me/${kind}/${id}`, body),
  remove: (kind: FactKind, id: string) => api.del(`/api/v1/profiles/me/${kind}/${id}`),
  verify: (kind: FactKind, id: string, verification: Verification) =>
    api.put(`/api/v1/profiles/me/${kind}/${id}/verification`, { verification }),
};
