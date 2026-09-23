import type { ReactNode } from "react";
import { NavLink, useLocation } from "react-router-dom";
import { useLogout, useSession } from "../hooks/auth-queries.js";
import { SessionUnavailable } from "./session-unavailable.js";

// Who is signed in, with sign-in and sign-out actions. Lives in the site
// navigation so every page shows the current session. A failed refresh keeps
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
      <span>Signed in as {session.data.account.username}</span>
      <button
        type="button"
        onClick={() => {
          logout.mutate();
        }}
        disabled={logout.isPending}
      >
        {logout.isPending ? "Signing out…" : "Sign out"}
      </button>
      <span role="status">
        {logoutStatus(logout.isPending, logout.isError)}
      </span>
      {refreshFailed}
    </>
  );
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
