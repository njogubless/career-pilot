/**
 * Navigation and module registry. Mirrors the Stitch sidebar groups.
 * `phase` is the roadmap phase that delivers the module (docs/DEVELOPMENT_ROADMAP.md);
 * modules not yet built render an honest "not available yet" page.
 */
export type NavItem = {
  path: string;
  label: string;
  icon: string;
  title: string; // breadcrumb leaf
  phase: number;
  summary: string;
  built: boolean;
};

export type NavGroup = { label: string; items: NavItem[] };

export const navGroups: NavGroup[] = [
  {
    label: "Portfolio Core",
    items: [
      { path: "/", label: "Overview", icon: "dashboard", title: "Overview", phase: 1, built: true,
        summary: "Career overview, priority opportunities, pipeline and agent activity." },
      { path: "/discover", label: "Discover Jobs", icon: "travel_explore", title: "Opportunity Directory", phase: 3, built: false,
        summary: "Search and rank jobs from approved public job boards with eligibility and match scores." },
      { path: "/intelligence", label: "AI Intelligence", icon: "psychology", title: "Intelligence Centre", phase: 8, built: false,
        summary: "Agent activity, workflow history, tool executions, approval requests and recovery." },
      { path: "/applications", label: "Applications", icon: "view_kanban", title: "Applications", phase: 6, built: false,
        summary: "Pipeline board and table, approvals, submission evidence and status history." },
      { path: "/outreach", label: "Recruiter Outreach", icon: "alternate_email", title: "Recruiter Outreach", phase: 7, built: false,
        summary: "Recruiter contacts, verification, approved email drafts, replies and follow-ups." },
      { path: "/analytics", label: "Career Analytics", icon: "analytics", title: "Career Analytics", phase: 9, built: false,
        summary: "Application funnel, source performance, recruiter engagement and skill gaps." },
    ],
  },
  {
    label: "Dossier & Systems",
    items: [
      { path: "/documents", label: "Documents & CVs", icon: "description", title: "Documents & CVs", phase: 2, built: false,
        summary: "Uploaded CVs, tailored versions and generated documents." },
      { path: "/profile", label: "Professional Profile", icon: "verified_user", title: "Professional Profile", phase: 2, built: false,
        summary: "Experience, education, skills and preferences, with verification status for every fact." },
      { path: "/automation", label: "Automation Centre", icon: "smart_toy", title: "Automation Centre", phase: 8, built: false,
        summary: "Automation modes, permissions, daily limits, schedules and the global kill switch." },
      { path: "/settings", label: "Settings", icon: "tune", title: "Settings", phase: 2, built: false,
        summary: "Account, security, integrations, notifications, privacy and data export." },
    ],
  },
];

export const allNavItems = navGroups.flatMap((g) => g.items);

export function findNavItem(pathname: string): NavItem | undefined {
  return allNavItems.find((i) => (i.path === "/" ? pathname === "/" : pathname.startsWith(i.path)));
}
