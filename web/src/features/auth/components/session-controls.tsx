import {
  useCallback,
  useEffect,
  useId,
  useRef,
  useState,
  type ReactNode,
  type RefObject,
} from "react";
import { Link, NavLink, useLocation } from "react-router-dom";
import { NavIcon } from "../../../app/nav-icons.js";
import type { KnownPermission, Permission } from "../api/auth-schemas.js";
import { useLogout, useSession } from "../hooks/auth-queries.js";
import { hasPermission, permissions } from "../permissions.js";
import { SessionUnavailable } from "./session-unavailable.js";

// Who is signed in, with sign-in and sign-out actions. Lives in the top bar
// so every page shows the current session. A failed refresh keeps
// showing the last known answer with a warning rather than discarding it.
export function SessionControls(): ReactNode {
  const session = useSession();
  const logout = useLogout();
  const location = useLocation();
  if (session.data === undefined) {
    // While a retry runs the query is pending again (there is no data to
    // keep), so the "Checking sign-in" status replaces the retry control and
    // a second click is impossible.
    return session.isError ? (
      <SessionUnavailable onRetry={session.refetch} />
    ) : (
      <span role="status">Checking sign-in…</span>
    );
  }
  const refreshFailed = session.isError ? (
    <RefreshFailed retrying={session.isFetching} onRetry={session.refetch} />
  ) : null;
  if (session.data === null) {
    // Remember where the person was so sign-in can bring them back.
    const from = `${location.pathname}${location.search}${location.hash}`;
    return (
      <>
        <NavLink to="/login" state={{ from }}>
          Sign in
        </NavLink>
        {refreshFailed}
      </>
    );
  }
  return (
    <>
      <AccountMenu
        username={session.data.account.username}
        granted={session.data.permissions}
        signingOut={logout.isPending}
        onSignOut={() => {
          logout.mutate();
        }}
      />
      <span role="status" className="session-status">
        {logoutStatus(logout.isPending, logout.isError)}
      </span>
      {refreshFailed}
    </>
  );
}

type AccountMenuProps = Readonly<{
  username: string;
  granted: readonly Permission[];
  signingOut: boolean;
  onSignOut: () => void;
}>;

// The signed-in account as one button that opens a small panel: the pages
// that belong to the person and sign-out. It is a disclosure, not an ARIA
// menu, so Tab moves through it like any other content. Escape, a click
// elsewhere, or a new page closes it.
function AccountMenu({
  username,
  granted,
  signingOut,
  onSignOut,
}: AccountMenuProps): ReactNode {
  // The panel remembers the page it was opened on, so moving to another
  // page closes it without an effect.
  const { pathname } = useLocation();
  const [openOn, setOpenOn] = useState<string | undefined>(undefined);
  const open = openOn === pathname;
  const root = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  const panelId = useId();
  const close = useCallback(() => {
    setOpenOn(undefined);
  }, []);
  useDismiss(open, root, button, close);
  return (
    <div className="account-menu" ref={root}>
      <button
        ref={button}
        type="button"
        className="account-button"
        aria-expanded={open}
        aria-controls={panelId}
        onClick={() => {
          setOpenOn(open ? undefined : pathname);
        }}
      >
        <span className="avatar" aria-hidden="true">
          {username.slice(0, 1).toUpperCase()}
        </span>
        {/* One run of text names the button; the visible name repeats it
            for sight only. */}
        <span className="visually-hidden">Signed in as {username}</span>
        <span className="account-name" aria-hidden="true">
          {username}
        </span>
        <NavIcon name="chevron" />
      </button>
      <div className="account-panel" id={panelId} hidden={!open}>
        <p className="account-panel-name">
          <span className="avatar" aria-hidden="true">
            {username.slice(0, 1).toUpperCase()}
          </span>
          {username}
        </p>
        <AccountLinks granted={granted} onNavigate={close} />
        <button
          type="button"
          className="btn-ghost account-sign-out"
          onClick={onSignOut}
          disabled={signingOut}
        >
          <NavIcon name="logout" />
          {signingOut ? "Signing out…" : "Sign out"}
        </button>
      </div>
    </div>
  );
}

type AccountLink = Readonly<{
  to: string;
  label: string;
  permission: KnownPermission;
}>;

// The pages that belong to the signed-in person, each behind the
// permission its page requires.
const ACCOUNT_LINKS: readonly AccountLink[] = [
  {
    to: "/statistics",
    label: "My statistics",
    permission: permissions.statsReadOwn,
  },
  {
    to: "/notifications",
    label: "My notifications",
    permission: permissions.notificationsManageOwn,
  },
];

// A link closes the panel itself: following it to the page already open
// changes no path, so the path alone would leave the panel open.
function AccountLinks({
  granted,
  onNavigate,
}: Readonly<{
  granted: readonly Permission[];
  onNavigate: () => void;
}>): ReactNode {
  const links = ACCOUNT_LINKS.filter((link) =>
    hasPermission(granted, link.permission),
  );
  if (links.length === 0) {
    return null;
  }
  return (
    <ul>
      {links.map((link) => (
        <li key={link.to}>
          <Link to={link.to} onClick={onNavigate}>
            {link.label}
          </Link>
        </li>
      ))}
    </ul>
  );
}

// Closes the panel on Escape, returning focus to its button, and on a
// pointer press outside it.
function useDismiss(
  open: boolean,
  root: RefObject<HTMLDivElement | null>,
  button: RefObject<HTMLButtonElement | null>,
  close: () => void,
): void {
  useEffect(() => {
    if (!open) {
      return undefined;
    }
    function onKey(event: KeyboardEvent): void {
      if (event.key === "Escape") {
        close();
        button.current?.focus();
      }
    }
    function onPointer(event: PointerEvent): void {
      const target = event.target;
      if (target instanceof Node && root.current?.contains(target) !== true) {
        close();
      }
    }
    document.addEventListener("keydown", onKey);
    document.addEventListener("pointerdown", onPointer);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("pointerdown", onPointer);
    };
  }, [open, root, button, close]);
}

type RefreshFailedProps = Readonly<{
  retrying: boolean;
  onRetry: () => Promise<unknown>;
}>;

// A background refresh failed but the last known answer is still shown. While
// a retry runs the cached answer stays, so the button itself reports progress.
function RefreshFailed({ retrying, onRetry }: RefreshFailedProps): ReactNode {
  return (
    <>
      <span role="alert">Sign-in status could not be refreshed.</span>
      <button
        type="button"
        disabled={retrying}
        onClick={() => {
          void onRetry();
        }}
      >
        {retrying ? "Retrying…" : "Retry"}
      </button>
      <span role="status">{retrying ? "Checking sign-in again." : ""}</span>
    </>
  );
}

function logoutStatus(pending: boolean, failed: boolean): string {
  if (pending) {
    return "Signing out, please wait.";
  }
  return failed ? "Sign-out failed. Please try again." : "";
}
