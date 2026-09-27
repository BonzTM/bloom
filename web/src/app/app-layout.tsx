import {
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
  type RefObject,
  type SyntheticEvent,
} from "react";
import {
  isRouteErrorResponse,
  Link,
  NavLink,
  Outlet,
  useLocation,
  useNavigate,
  useRouteError,
} from "react-router-dom";
import { PermissionGate } from "../features/auth/components/permission-gate.js";
import { SessionControls } from "../features/auth/components/session-controls.js";
import type { KnownPermission } from "../features/auth/api/auth-schemas.js";
import {
  ADMIN_PERMISSIONS,
  permissions,
  REQUESTS_PERMISSIONS,
} from "../features/auth/permissions.js";
import { NavIcon, type IconName } from "./nav-icons.js";
import { RouteErrorBoundary } from "./route-error-boundary.js";
import { useRouteFocus } from "./use-route-focus.js";

// The app shell: a sidebar with the brand and the navigation, a top bar
// with search and the session controls, and the page. On narrow screens the
// sidebar becomes a drawer opened from the top bar; there is one navigation
// and one set of names either way.
export function AppLayout(): ReactNode {
  const main = useRef<HTMLElement>(null);
  const bar = useRef<HTMLElement>(null);
  const [open, setOpen] = useState(false);
  const location = useLocation();
  const navId = useId();
  useRouteFocus(main);
  useBarHeight(bar);
  useCloseOnNavigate(location.pathname, setOpen);
  return (
    <div className={open ? "shell nav-open" : "shell"}>
      <aside className="sidebar" id={navId}>
        <Link to="/" className="brand">
          <span className="brand-mark" aria-hidden="true" />
          Bloom
        </Link>
        <nav aria-label="Main navigation">
          <ul>
            <NavItem to="/" icon="home" end>
              Home
            </NavItem>
            <PermissionGate anyOf={REQUESTS_PERMISSIONS}>
              <NavItem to="/requests" icon="compass">
                Requests
              </NavItem>
            </PermissionGate>
            <PermissionGate anyOf={[permissions.statsReadOwn]}>
              <NavItem to="/statistics" icon="chart">
                My statistics
              </NavItem>
            </PermissionGate>
            <NavItem to="/about" icon="info">
              About
            </NavItem>
          </ul>
          <PermissionGate anyOf={ADMIN_PERMISSIONS}>
            <p className="nav-section-label" id={`${navId}-admin`}>
              Administration
            </p>
            <ul aria-labelledby={`${navId}-admin`}>
              <NavItem to="/admin" icon="shield" end>
                Admin
              </NavItem>
              {ADMIN_LINKS.map((link) => (
                <PermissionGate key={link.to} anyOf={link.anyOf}>
                  <NavItem
                    to={link.to}
                    icon={link.icon}
                    section="Administration"
                  >
                    {link.label}
                  </NavItem>
                </PermissionGate>
              ))}
            </ul>
          </PermissionGate>
        </nav>
      </aside>
      <div className="content">
        <header className="topbar" ref={bar}>
          <button
            type="button"
            className="btn-ghost nav-toggle"
            aria-expanded={open}
            aria-controls={navId}
            onClick={() => {
              setOpen((current) => !current);
            }}
          >
            <NavIcon name="menu" />
            <span className="visually-hidden">Menu</span>
          </button>
          <PermissionGate anyOf={[permissions.requestsCreate]}>
            <GlobalSearch />
          </PermissionGate>
          <div className="topbar-session session-controls">
            <SessionControls />
          </div>
        </header>
        <main ref={main} tabIndex={-1}>
          <RouteErrorBoundary>
            <Outlet />
          </RouteErrorBoundary>
        </main>
      </div>
    </div>
  );
}

type AdminLink = Readonly<{
  to: string;
  label: string;
  icon: IconName;
  anyOf: readonly KnownPermission[];
}>;

const ADMIN_LINKS: readonly AdminLink[] = [
  {
    to: "/admin/requests",
    label: "Requests",
    icon: "inbox",
    anyOf: [permissions.requestsApprove],
  },
  {
    to: "/admin/playback",
    label: "Playback",
    icon: "play",
    anyOf: [permissions.statsReadAll],
  },
  {
    to: "/admin/statistics",
    label: "Statistics",
    icon: "chart",
    anyOf: [permissions.statsReadAll],
  },
  {
    to: "/admin/media-servers",
    label: "Media servers",
    icon: "server",
    anyOf: [permissions.adminSettings],
  },
  {
    to: "/admin/download-managers",
    label: "Download managers",
    icon: "download",
    anyOf: [permissions.adminSettings],
  },
  {
    to: "/admin/request-profiles",
    label: "Request settings",
    icon: "sliders",
    anyOf: [permissions.adminSettings],
  },
  {
    to: "/admin/notifications",
    label: "Notifications",
    icon: "bell",
    anyOf: [permissions.adminSettings],
  },
  {
    to: "/admin/invites",
    label: "Invites",
    icon: "ticket",
    anyOf: [permissions.usersInvite],
  },
  {
    to: "/admin/roles",
    label: "Roles",
    icon: "users",
    anyOf: [permissions.adminRoles],
  },
];

function NavItem({
  to,
  icon,
  end,
  section,
  children,
}: Readonly<{
  to: string;
  icon: IconName;
  end?: boolean;
  // Spoken before the label so a section link never shares a name with a
  // page's own link of the same title.
  section?: string;
  children: ReactNode;
}>): ReactNode {
  return (
    <li>
      <NavLink to={to} end={end === true}>
        <NavIcon name={icon} />
        {section === undefined ? null : (
          <span className="visually-hidden">{section}: </span>
        )}
        <span>{children}</span>
      </NavLink>
    </li>
  );
}

// One search box for the whole app: it lands on the requests page with the
// query, which owns the results and their states.
function GlobalSearch(): ReactNode {
  const navigate = useNavigate();
  const id = useId();
  function handleSubmit(event: SyntheticEvent<HTMLFormElement>): void {
    event.preventDefault();
    const value = new FormData(event.currentTarget).get("q");
    const query = typeof value === "string" ? value.trim() : "";
    if (query === "") {
      return;
    }
    void navigate(`/requests?q=${encodeURIComponent(query)}`);
  }
  return (
    <form role="search" className="global-search" onSubmit={handleSubmit}>
      <label htmlFor={id} className="visually-hidden">
        Search movies and series
      </label>
      <NavIcon name="search" />
      <input
        id={id}
        name="q"
        type="search"
        placeholder="Search movies and series"
        autoComplete="off"
        maxLength={200}
      />
    </form>
  );
}

function useCloseOnNavigate(
  pathname: string,
  setOpen: (open: boolean) => void,
): void {
  const previous = useRef(pathname);
  useLayoutEffect(() => {
    if (previous.current !== pathname) {
      previous.current = pathname;
      setOpen(false);
    }
  }, [pathname, setOpen]);
}

// The top bar is sticky and its height depends on how its controls wrap, so
// the measured height is written to a custom property that scroll padding
// reads: focus and anchors then always land below it.
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
