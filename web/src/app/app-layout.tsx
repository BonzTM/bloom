import { useLayoutEffect, useRef, type ReactNode, type RefObject } from "react";
import {
  isRouteErrorResponse,
  Link,
  NavLink,
  Outlet,
  useRouteError,
} from "react-router-dom";
import { PermissionGate } from "../features/auth/components/permission-gate.js";
import { SessionControls } from "../features/auth/components/session-controls.js";
import {
  ADMIN_PERMISSIONS,
  permissions,
  REQUESTS_PERMISSIONS,
} from "../features/auth/permissions.js";
import { RouteErrorBoundary } from "./route-error-boundary.js";
import { useRouteFocus } from "./use-route-focus.js";

// The app shell: a sidebar with the brand, the navigation, and the session
// controls, beside the page. On narrow screens the sidebar becomes a top bar
// through CSS alone, so there is one navigation and one set of names.
export function AppLayout(): ReactNode {
  const main = useRef<HTMLElement>(null);
  const bar = useRef<HTMLElement>(null);
  useRouteFocus(main);
  useBarHeight(bar);
  return (
    <div className="shell">
      <header className="sidebar" ref={bar}>
        <Link to="/" className="brand">
          <span className="brand-mark" aria-hidden="true" />
          Bloom
        </Link>
        <nav aria-label="Main navigation">
          <ul>
            <li>
              <NavLink to="/" end>
                Home
              </NavLink>
            </li>
            <li>
              <NavLink to="/about">About</NavLink>
            </li>
            <PermissionGate anyOf={REQUESTS_PERMISSIONS}>
              <li>
                <NavLink to="/requests">Requests</NavLink>
              </li>
            </PermissionGate>
            <PermissionGate anyOf={[permissions.statsReadOwn]}>
              <li>
                <NavLink to="/statistics">My statistics</NavLink>
              </li>
            </PermissionGate>
            <PermissionGate anyOf={ADMIN_PERMISSIONS}>
              <li>
                <NavLink to="/admin">Admin</NavLink>
              </li>
            </PermissionGate>
          </ul>
        </nav>
        <div className="sidebar-footer session-controls">
          <SessionControls />
        </div>
      </header>
      <div className="content">
        <main ref={main} tabIndex={-1}>
          <RouteErrorBoundary>
            <Outlet />
          </RouteErrorBoundary>
        </main>
      </div>
    </div>
  );
}

// On narrow screens the bar is sticky and its height depends on how the
// navigation and session controls wrap, so the measured height is written
// to a custom property that scroll padding reads: focus and anchors then
// always land below it.
function useBarHeight(bar: RefObject<HTMLElement | null>): void {
  useLayoutEffect(() => {
    const element = bar.current;
    if (element === null || typeof ResizeObserver === "undefined") {
      return undefined;
    }
    const root = document.documentElement;
    const apply = (): void => {
      root.style.setProperty(
        "--bar-height",
        `${String(Math.ceil(element.getBoundingClientRect().height))}px`,
      );
    };
    apply();
    const observer = new ResizeObserver(apply);
    observer.observe(element);
    return () => {
      observer.disconnect();
      root.style.removeProperty("--bar-height");
    };
  }, [bar]);
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
      <p>
        <Link to="/">Return to home</Link>
      </p>
    </main>
  );
}

function isNotFoundError(error: unknown): boolean {
  return isRouteErrorResponse(error) && error.status === 404;
}
