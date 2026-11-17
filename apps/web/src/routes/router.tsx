import type { RouteObject } from "react-router";
import { AppShell } from "../components/shell/AppShell";
import { allNavItems } from "../components/shell/nav";
import { ApplicationDetailPage } from "./applications/ApplicationDetailPage";
import { ApplicationsPage } from "./applications/ApplicationsPage";
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
import { DocumentEditorPage } from "./preparation/DocumentEditorPage";
import { PrintPage } from "./preparation/PrintPage";
import { ProfilePage } from "./profile/ProfilePage";
import { SettingsPage } from "./settings/SettingsPage";

export const routes: RouteObject[] = [
  { path: "/login", element: <LoginPage /> },
  { path: "/register", element: <RegisterPage /> },
  { path: "/forgot-password", element: <ForgotPasswordPage /> },
  { path: "/reset-password", element: <ResetPasswordPage /> },
  {
    // Print layout without the app shell (browser "Save as PDF").
    path: "/prepare/:id/print",
    element: (
      <RequireAuth>
        <PrintPage />
      </RequireAuth>
    ),
  },
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
      { path: "prepare/:id", element: <DocumentEditorPage /> },
      { path: "applications", element: <ApplicationsPage /> },
      { path: "applications/:id", element: <ApplicationDetailPage /> },
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
