import type { ReactNode } from "react";
import { NavLink } from "react-router-dom";
import { useLogout, useSession } from "../hooks/auth-queries.js";

// The sign-in link or the signed-in account with a sign-out button. Lives in
// the site navigation so every page shows who is signed in.
export function SessionMenu(): ReactNode {
  const session = useSession();
  const logout = useLogout();
  if (session.isPending) {
    return <span role="status">Checking sign-in…</span>;
  }
  if (session.isError || session.data === null) {
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
        aria-disabled={logout.isPending}
      >
        {logout.isPending ? "Signing out…" : "Sign out"}
      </button>
    </>
  );
}
