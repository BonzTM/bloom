import type { ReactNode } from "react";
import { NavLink } from "react-router-dom";
import { useLogout, useSession } from "../hooks/auth-queries.js";

// Who is signed in, with sign-in and sign-out actions. Lives in the site
// navigation so every page shows the current session.
export function SessionControls(): ReactNode {
  const session = useSession();
  const logout = useLogout();
  if (session.isPending) {
    return <span role="status">Checking sign-in…</span>;
  }
  if (session.isError) {
    return (
      <>
        <span role="alert">Sign-in status is unavailable.</span>
        <button
          type="button"
          onClick={() => {
            void session.refetch();
          }}
        >
          Retry
        </button>
      </>
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
        {logout.isError ? "Sign-out failed. Please try again." : ""}
      </span>
    </>
  );
}
