import type { ReactNode } from "react";
import { Link, useLocation } from "react-router-dom";
import { acceptedInviteSchema } from "../features/invites/api/invites-schemas.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

export const INVITE_ACCEPTED_PATH = "/invite/accepted";

// The confirmation after an invite was accepted, at an address that carries
// no invite code. The details travel in router state; arriving here without
// them (a reload, a bookmark) still gets a truthful, if plainer, answer.
export default function InviteAcceptedRoute(): ReactNode {
  usePageTitle(pageTitle("Account ready"));
  const location = useLocation();
  const accepted = acceptedFromState(location.state);
  return (
    <div className="narrow">
      <h1>Your account is ready</h1>
      {accepted === undefined ? (
        <p role="status">
          Sign in to your media server with the username and password you chose.
        </p>
      ) : (
        <p role="status">
          Sign in to {accepted.media_server_name} as{" "}
          <strong>{accepted.username}</strong> with the password you chose.
        </p>
      )}
      <p>
        <Link to="/">Back to Bloom</Link>
      </p>
    </div>
  );
}

function acceptedFromState(state: unknown) {
  if (typeof state !== "object" || state === null || !("accepted" in state)) {
    return undefined;
  }
  const parsed = acceptedInviteSchema.safeParse(state.accepted);
  return parsed.success ? parsed.data : undefined;
}
