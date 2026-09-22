import type { ReactNode } from "react";
import { NavLink } from "react-router-dom";
import { useLogout, useSession } from "../hooks/auth-queries.js";

// Who is signed in, with sign-in and sign-out actions. Lives in the site
// navigation so every page shows the current session. A failed refresh keeps
// showing the last known account with a warning rather than discarding it.
export function SessionControls(): ReactNode {
  const session = useSession();
  const logout = useLogout();
  if (session.data === undefined) {
    return session.isError ? (
      <SessionUnavailable onRetry={session.refetch} />
    ) : (
      <span role="status">Checking sign-in…</span>
    );
  }
  if (session.data === null) {
    return <NavLink to="/login">Sign in</NavLink>;
  }
  return (
    <>
      <span>Signed in as {session.data.username}</span>
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
      {session.isError ? (
        <span role="alert">Sign-in status could not be refreshed.</span>
      ) : null}
    </>
  );
}

function SessionUnavailable({
  onRetry,
}: Readonly<{ onRetry: () => Promise<unknown> }>): ReactNode {
  return (
    <>
      <span role="alert">Sign-in status is unavailable.</span>
      <button
        type="button"
        onClick={() => {
          void onRetry();
        }}
      >
        Retry
      </button>
    </>
  );
}

function logoutStatus(pending: boolean, failed: boolean): string {
  if (pending) {
    return "Signing out, please wait.";
  }
  return failed ? "Sign-out failed. Please try again." : "";
}
