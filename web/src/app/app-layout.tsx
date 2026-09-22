import type { ReactNode } from "react";
import {
  isRouteErrorResponse,
  Link,
  NavLink,
  Outlet,
  useRouteError,
} from "react-router-dom";
import { SessionMenu } from "../features/auth/components/session-menu.js";
import { RouteErrorBoundary } from "./route-error-boundary.js";

export function AppLayout(): ReactNode {
  return (
    <>
      <header>
        <nav aria-label="Main navigation">
          <ul>
            <li>
              <NavLink to="/" end>
                Bloom
              </NavLink>
            </li>
            <li>
              <NavLink to="/about">About</NavLink>
            </li>
            <li className="session-menu">
              <SessionMenu />
            </li>
          </ul>
        </nav>
      </header>
      <main>
        <RouteErrorBoundary>
          <Outlet />
        </RouteErrorBoundary>
      </main>
    </>
  );
}

export function RouterErrorPage(): ReactNode {
  const error: unknown = useRouteError();
  const notFound = isNotFoundError(error);
  return (
    <main>
      <h1>{notFound ? "Page not found" : "Something went wrong"}</h1>
      <p role="alert">
        {notFound
          ? "The requested page does not exist."
          : "The page could not be loaded."}
      </p>
      <Link to="/">Return to home</Link>
    </main>
  );
}

function isNotFoundError(error: unknown): boolean {
  return isRouteErrorResponse(error) && error.status === 404;
}
