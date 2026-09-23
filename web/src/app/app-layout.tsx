import { useRef, type ReactNode } from "react";
import {
  isRouteErrorResponse,
  Link,
  NavLink,
  Outlet,
  useRouteError,
} from "react-router-dom";
import { PermissionGate } from "../features/auth/components/permission-gate.js";
import { SessionControls } from "../features/auth/components/session-controls.js";
import { ADMIN_PERMISSIONS } from "../features/auth/permissions.js";
import { RouteErrorBoundary } from "./route-error-boundary.js";
import { useRouteFocus } from "./use-route-focus.js";

export function AppLayout(): ReactNode {
  const main = useRef<HTMLElement>(null);
  useRouteFocus(main);
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
            <PermissionGate anyOf={ADMIN_PERMISSIONS}>
              <li>
                <NavLink to="/admin">Admin</NavLink>
              </li>
            </PermissionGate>
            <li className="session-controls">
              <SessionControls />
            </li>
          </ul>
        </nav>
      </header>
      <main ref={main} tabIndex={-1}>
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
