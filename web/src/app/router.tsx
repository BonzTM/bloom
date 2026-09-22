import { lazy, Suspense, type ReactNode } from "react";
import {
  createBrowserRouter,
  createMemoryRouter,
  type RouteObject,
} from "react-router-dom";
import { AsyncStatus } from "../components/async-status.js";
import { HomeRoute } from "../routes/home-route.js";
import { NotFoundRoute } from "../routes/not-found-route.js";
import { AppLayout, RouterErrorPage } from "./app-layout.js";

const LazyAboutRoute = lazy(() => import("../routes/about-route.js"));

function AboutBoundary(): ReactNode {
  return (
    <Suspense fallback={<AsyncStatus>Loading about page…</AsyncStatus>}>
      <LazyAboutRoute />
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
      { path: "about", element: <AboutBoundary /> },
      { path: "*", element: <NotFoundRoute /> },
    ],
  },
];

export function createAppRouter() {
  return createBrowserRouter(routes);
}

export function createTestRouter(initialEntries: readonly string[]) {
  return createMemoryRouter(routes, { initialEntries: [...initialEntries] });
}
