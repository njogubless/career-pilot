import type { RouteObject } from "react-router";
import { AppShell } from "../components/shell/AppShell";
import { allNavItems } from "../components/shell/nav";
import { ForgotPasswordPage, ResetPasswordPage } from "./auth/PasswordPages";
import { LoginPage } from "./auth/LoginPage";
import { RegisterPage } from "./auth/RegisterPage";
import { RequireAuth } from "./auth/RequireAuth";
import { DiscoverPage } from "./discover/DiscoverPage";
import { JobDetailPage } from "./discover/JobDetailPage";
import { DocumentsPage } from "./documents/DocumentsPage";
import { ModulePendingPage } from "./ModulePendingPage";
import { NotFoundPage } from "./NotFoundPage";
import { OverviewPage } from "./OverviewPage";
import { ProfilePage } from "./profile/ProfilePage";
import { SettingsPage } from "./settings/SettingsPage";

export const routes: RouteObject[] = [
  { path: "/login", element: <LoginPage /> },
  { path: "/register", element: <RegisterPage /> },
  { path: "/forgot-password", element: <ForgotPasswordPage /> },
  { path: "/reset-password", element: <ResetPasswordPage /> },
  {
    path: "/",
    element: (
      <RequireAuth>
        <AppShell />
      </RequireAuth>
    ),
    children: [
      { index: true, element: <OverviewPage /> },
      { path: "discover", element: <DiscoverPage /> },
      { path: "jobs/:id", element: <JobDetailPage /> },
      { path: "profile", element: <ProfilePage /> },
      { path: "documents", element: <DocumentsPage /> },
      { path: "settings", element: <SettingsPage /> },
      ...allNavItems
        .filter((i) => !i.built)
        .map((item) => ({ path: item.path.slice(1), element: <ModulePendingPage item={item} /> })),
      { path: "*", element: <NotFoundPage /> },
    ],
  },
];
