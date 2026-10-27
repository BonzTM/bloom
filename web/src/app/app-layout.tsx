import {
  useEffect,
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
  Outlet,
  useLocation,
  useNavigate,
  useRouteError,
} from "react-router-dom";
import { PermissionGate } from "../features/auth/components/permission-gate.js";
import { SessionControls } from "../features/auth/components/session-controls.js";
import { useSession } from "../features/auth/hooks/auth-queries.js";
import { permissions } from "../features/auth/permissions.js";
import { VersionBadge } from "../features/system/components/version-badge.js";
import { NavIcon, type IconName } from "./nav-icons.js";
import {
  groupFor,
  isHomeArea,
  NAV_GROUPS,
  normalizePath,
  pageFor,
  visiblePages,
  type NavPage,
} from "./nav-model.js";
import { RouteErrorBoundary } from "./route-error-boundary.js";
import { useRouteFocus } from "./use-route-focus.js";

// The app shell: a sidebar with the brand and one short list of areas, a
// top bar with search and the account menu, and the page. An area with
// several pages shows them as tabs above the page. On narrow screens the
// sidebar becomes a drawer opened from the top bar; there is one navigation
// and one set of names either way.
export function AppLayout(): ReactNode {
  const main = useRef<HTMLElement>(null);
  const bar = useRef<HTMLElement>(null);
  const sidebar = useRef<HTMLElement>(null);
  const toggle = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  const location = useLocation();
  const navId = useId();
  useRouteFocus(main);
  useBarHeight(bar);
  useCloseOnNavigate(location.pathname, setOpen);
  useDrawerFocus(open, setOpen, sidebar, toggle);
  return (
    <div className={open ? "shell nav-open" : "shell"}>
      <aside className="sidebar" id={navId} ref={sidebar}>
        <Link to="/" className="brand">
          <span className="brand-mark" aria-hidden="true" />
          Bloom
        </Link>
        <Sidebar
          pathname={normalizePath(location.pathname)}
          onNavigate={() => {
            setOpen(false);
          }}
        />
        <div className="sidebar-footer">
          <VersionBadge quiet />
        </div>
      </aside>
      <div className="content">
        <header className="topbar" ref={bar}>
          <button
            ref={toggle}
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
          <SectionTabs pathname={normalizePath(location.pathname)} />
          <RouteErrorBoundary>
            <Outlet />
          </RouteErrorBoundary>
        </main>
      </div>
    </div>
  );
}

// Home, each area the account may open, and About. An area is current when
// the page is any of its pages or beneath one, such as a title under
// Discover or a watch under Playback. Following a link closes the drawer
// even when it leads to the page already open.
function Sidebar({
  pathname,
  onNavigate,
}: Readonly<{ pathname: string; onNavigate: () => void }>): ReactNode {
  const session = useSession();
  const granted = session.data?.permissions ?? [];
  return (
    <nav aria-label="Main navigation">
      <ul>
        <SidebarLink
          to="/"
          icon="home"
          current={isHomeArea(pathname)}
          onNavigate={onNavigate}
        >
          Home
        </SidebarLink>
        {NAV_GROUPS.map((group) => {
          const first = visiblePages(group, granted)[0];
          return first === undefined ? null : (
            <SidebarLink
              key={group.label}
              to={first.to}
              icon={group.icon}
              current={pageFor(group, pathname) !== undefined}
              onNavigate={onNavigate}
            >
              {group.label}
            </SidebarLink>
          );
        })}
        <SidebarLink
          to="/about"
          icon="info"
          current={pathname === "/about"}
          onNavigate={onNavigate}
        >
          About
        </SidebarLink>
      </ul>
    </nav>
  );
}

type SidebarLinkProps = Readonly<{
  to: string;
  icon: IconName;
  current: boolean;
  onNavigate: () => void;
  children: ReactNode;
}>;

function SidebarLink({
  to,
  icon,
  current,
  onNavigate,
  children,
}: SidebarLinkProps): ReactNode {
  const { pathname } = useLocation();
  const exact = normalizePath(pathname) === to;
  return (
    <li>
      <Link
        to={to}
        className={current ? "active" : undefined}
        aria-current={sidebarCurrent(current, exact)}
        onClick={onNavigate}
      >
        <NavIcon name={icon} />
        <span>{children}</span>
      </Link>
    </li>
  );
}

// The link of the open page is the current page; an area's link while one
// of its other pages is open marks the current location instead.
function sidebarCurrent(
  current: boolean,
  exact: boolean,
): "page" | "location" | undefined {
  if (!current) {
    return undefined;
  }
  return exact ? "page" : "location";
}

// The pages of the current area as tabs, shown only when the account may
// open more than one of them. The tab of the open page is marked current;
// a page beneath it, such as one watch, marks its tab as the location.
function SectionTabs({ pathname }: Readonly<{ pathname: string }>): ReactNode {
  const session = useSession();
  const group = groupFor(pathname);
  if (
    group === undefined ||
    session.data === undefined ||
    session.data === null
  ) {
    return null;
  }
  const pages = visiblePages(group, session.data.permissions);
  if (pages.length < 2) {
    return null;
  }
  const current = pageFor(group, pathname);
  return (
    <nav className="section-tabs" aria-label={`${group.label} pages`}>
      <ul>
        {pages.map((page) => (
          <li key={page.to}>
            <Link
              to={page.to}
              className={page === current ? "active" : undefined}
              aria-current={tabCurrent(page, current, pathname)}
            >
              {page.label}
            </Link>
          </li>
        ))}
      </ul>
    </nav>
  );
}

function tabCurrent(
  page: NavPage,
  current: NavPage | undefined,
  pathname: string,
): "page" | "location" | undefined {
  if (page !== current) {
    return undefined;
  }
  return pathname === page.to ? "page" : "location";
}

// One search box for the whole app: it lands on the search page with the
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
    void navigate(`/search?q=${encodeURIComponent(query)}`);
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

// On narrow screens the drawer opens from a button that follows it in the
// page, so opening it moves focus to its first link; Escape closes it and
// returns focus to the button.
function useDrawerFocus(
  open: boolean,
  setOpen: (open: boolean) => void,
  sidebar: RefObject<HTMLElement | null>,
  toggle: RefObject<HTMLButtonElement | null>,
): void {
  useEffect(() => {
    if (!open) {
      return undefined;
    }
    sidebar.current?.querySelector<HTMLElement>("nav a[href]")?.focus();
    function onKey(event: KeyboardEvent): void {
      if (event.key === "Escape") {
        setOpen(false);
        toggle.current?.focus();
      }
    }
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("keydown", onKey);
    };
  }, [open, setOpen, sidebar, toggle]);
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
