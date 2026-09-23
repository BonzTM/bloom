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
