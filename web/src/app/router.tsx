import { lazy, Suspense, type ReactNode } from "react";
import {
  createBrowserRouter,
  createMemoryRouter,
  Outlet,
  type InitialEntry,
  type RouteObject,
} from "react-router-dom";
import { AsyncStatus } from "../components/async-status.js";
import {
  ADMIN_PERMISSIONS,
  permissions,
} from "../features/auth/permissions.js";
import { HomeRoute } from "../routes/home-route.js";
import { NotFoundRoute } from "../routes/not-found-route.js";
import { RequirePermission } from "../routes/require-permission.js";
import { AppLayout, RouterErrorPage } from "./app-layout.js";

const LazyAboutRoute = lazy(() => import("../routes/about-route.js"));
const LazyLoginRoute = lazy(() => import("../routes/login-route.js"));
const LazyAdminRoute = lazy(() => import("../routes/admin-route.js"));
const LazyRolesRoute = lazy(() => import("../routes/roles-route.js"));
const LazyMediaServersRoute = lazy(
  () => import("../routes/media-servers-route.js"),
);
const LazyInvitesRoute = lazy(() => import("../routes/invites-route.js"));
const LazyPlaybackRoute = lazy(() => import("../routes/playback-route.js"));
const LazyStatisticsRoute = lazy(() => import("../routes/statistics-route.js"));
const LazyUserStatsRoute = lazy(() => import("../routes/user-stats-route.js"));
const LazyRequestsAdminRoute = lazy(
  () => import("../routes/requests-admin-route.js"),
);
const LazyDiscoverRoute = lazy(() => import("../routes/discover-route.js"));
const LazyTitleRoute = lazy(() => import("../routes/title-route.js"));
const LazyDownloadManagersRoute = lazy(
  () => import("../routes/download-managers-route.js"),
);
const LazyRequestProfilesRoute = lazy(
  () => import("../routes/request-profiles-route.js"),
);
const LazyInviteAcceptRoute = lazy(
  () => import("../routes/invite-accept-route.js"),
);
const LazyInviteAcceptedRoute = lazy(
  () => import("../routes/invite-accepted-route.js"),
);

type LazyPageProps = Readonly<{ loading: string; children: ReactNode }>;

function LazyPage({ loading, children }: LazyPageProps): ReactNode {
  return (
    <Suspense fallback={<AsyncStatus>{loading}</AsyncStatus>}>
      {children}
    </Suspense>
  );
}

const routes: RouteObject[] = [
  {
    path: "/",
    element: <AppLayout />,
    errorElement: <RouterErrorPage />,
    children: [
      { index: true, element: <HomeRoute /> },
      {
        path: "about",
        element: (
          <LazyPage loading="Loading about page…">
            <LazyAboutRoute />
          </LazyPage>
        ),
      },
      {
        path: "login",
        element: (
          <LazyPage loading="Loading sign-in page…">
            <LazyLoginRoute />
          </LazyPage>
        ),
      },
      {
        path: "invite/accepted",
        element: (
          <LazyPage loading="Loading…">
            <LazyInviteAcceptedRoute />
          </LazyPage>
        ),
      },
      {
        path: "invite/:code",
        element: (
          <LazyPage loading="Loading your invite…">
            <LazyInviteAcceptRoute />
          </LazyPage>
        ),
      },
      {
        path: "requests",
        element: (
          <RequirePermission
            anyOf={[permissions.requestsCreate, permissions.requestsReadOwn]}
          >
            <LazyPage loading="Loading requests…">
              <LazyDiscoverRoute />
            </LazyPage>
          </RequirePermission>
        ),
      },
      {
        path: "requests/:kind/:id",
        element: (
          <RequirePermission anyOf={[permissions.requestsCreate]}>
            <LazyPage loading="Loading the title…">
              <LazyTitleRoute />
            </LazyPage>
          </RequirePermission>
        ),
      },
      {
        path: "admin",
        element: (
          <RequirePermission anyOf={ADMIN_PERMISSIONS}>
            <Outlet />
          </RequirePermission>
        ),
        children: [
          {
            index: true,
            element: (
              <LazyPage loading="Loading administration…">
                <LazyAdminRoute />
              </LazyPage>
            ),
          },
          {
            path: "roles",
            element: (
              <RequirePermission anyOf={[permissions.adminRoles]}>
                <LazyPage loading="Loading roles…">
                  <LazyRolesRoute />
                </LazyPage>
              </RequirePermission>
            ),
          },
          {
            path: "media-servers",
            element: (
              <RequirePermission anyOf={[permissions.adminSettings]}>
                <LazyPage loading="Loading media servers…">
                  <LazyMediaServersRoute />
                </LazyPage>
              </RequirePermission>
            ),
          },
          {
            path: "invites",
            element: (
              <RequirePermission anyOf={[permissions.usersInvite]}>
                <LazyPage loading="Loading invites…">
                  <LazyInvitesRoute />
                </LazyPage>
              </RequirePermission>
            ),
          },
          {
            path: "playback",
            element: (
              <RequirePermission anyOf={[permissions.statsReadAll]}>
                <LazyPage loading="Loading playback…">
                  <LazyPlaybackRoute />
                </LazyPage>
              </RequirePermission>
            ),
          },
          {
            path: "statistics",
            element: (
              <RequirePermission anyOf={[permissions.statsReadAll]}>
                <LazyPage loading="Loading statistics…">
                  <LazyStatisticsRoute />
                </LazyPage>
              </RequirePermission>
            ),
          },
          {
            path: "statistics/users/:serverId/:userId",
            element: (
              <RequirePermission anyOf={[permissions.statsReadAll]}>
                <LazyPage loading="Loading statistics…">
                  <LazyUserStatsRoute />
                </LazyPage>
              </RequirePermission>
            ),
          },
          {
            path: "requests",
            element: (
              <RequirePermission anyOf={[permissions.requestsApprove]}>
                <LazyPage loading="Loading requests…">
                  <LazyRequestsAdminRoute />
                </LazyPage>
              </RequirePermission>
            ),
          },
          {
            path: "download-managers",
            element: (
              <RequirePermission anyOf={[permissions.adminSettings]}>
                <LazyPage loading="Loading download managers…">
                  <LazyDownloadManagersRoute />
                </LazyPage>
              </RequirePermission>
            ),
          },
          {
            path: "request-profiles",
            element: (
              <RequirePermission anyOf={[permissions.adminSettings]}>
                <LazyPage loading="Loading request settings…">
                  <LazyRequestProfilesRoute />
                </LazyPage>
              </RequirePermission>
            ),
          },
        ],
      },
      { path: "*", element: <NotFoundRoute /> },
    ],
  },
];

export function createAppRouter() {
  return createBrowserRouter(routes);
}

export function createTestRouter(initialEntries: readonly InitialEntry[]) {
  return createMemoryRouter(routes, { initialEntries: [...initialEntries] });
}
