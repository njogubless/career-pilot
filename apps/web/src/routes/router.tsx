import type { RouteObject } from "react-router";
import { AppShell } from "../components/shell/AppShell";
import { allNavItems } from "../components/shell/nav";
import { ModulePendingPage } from "./ModulePendingPage";
import { NotFoundPage } from "./NotFoundPage";
import { OverviewPage } from "./OverviewPage";

export const routes: RouteObject[] = [
  {
    path: "/",
    element: <AppShell />,
    children: [
      { index: true, element: <OverviewPage /> },
      ...allNavItems
        .filter((i) => !i.built)
        .map((item) => ({ path: item.path.slice(1), element: <ModulePendingPage item={item} /> })),
      { path: "*", element: <NotFoundPage /> },
    ],
  },
];
