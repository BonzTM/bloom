import type { ReactNode } from "react";
import { AsyncStatus } from "../components/async-status.js";
import {
  useSession,
  useSessionRecheck,
} from "../features/auth/hooks/auth-queries.js";
import { RolesTable } from "../features/roles/components/roles-table.js";
import { useRoles } from "../features/roles/hooks/roles-queries.js";
import { accessDenial } from "../lib/api/errors.js";
import { AccessDeniedRoute } from "./access-denied-route.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

export default function RolesRoute(): ReactNode {
  usePageTitle(pageTitle("Roles"));
  const session = useSession();
  const roles = useRoles(session.data?.account.id);
  const denial = accessDenial(roles.error);
  // The server says the session is gone although the cache says signed in.
  // Re-checking the session lets the route guard send the person to sign in;
  // if the server then still reports a live session, the retry below stands.
  useSessionRecheck(denial === "unauthenticated", roles.errorUpdatedAt);
  if (denial === "forbidden") {
    return <AccessDeniedRoute />;
  }
  return (
    <>
      <h1>Roles</h1>
      <p>
        A role is a named set of permissions. Built-in roles ship with Bloom.
      </p>
      {denial === "unauthenticated" ? (
        <SignInNotConfirmed onRetry={roles.refetch} />
      ) : (
        <RolesTable query={roles} />
      )}
    </>
  );
}

function SignInNotConfirmed({
  onRetry,
}: Readonly<{ onRetry: () => Promise<unknown> }>): ReactNode {
  return (
    <>
      <AsyncStatus kind="alert">
        The roles could not be loaded because your sign-in could not be
        confirmed.
      </AsyncStatus>
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
