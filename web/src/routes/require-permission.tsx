import type { ReactNode } from "react";
import { useLocation } from "react-router-dom";
import type { KnownPermission } from "../features/auth/api/auth-schemas.js";
import { useSession } from "../features/auth/hooks/auth-queries.js";
import { hasAnyPermission } from "../features/auth/permissions.js";
import { AccessDeniedRoute } from "./access-denied-route.js";
import { useRedirectOnce } from "./use-redirect-once.js";

type RequirePermissionProps = Readonly<{
  anyOf: readonly KnownPermission[];
  children: ReactNode;
}>;

// Route guard. A signed-out visitor is sent to sign in and brought back here
// afterwards; a signed-in account without any of the permissions sees a
// refusal. The server still authorizes every request this page makes, so the
// guard only decides what the page shows. The session check itself is
// announced and retried from the navigation, which is on every page; the
// guard adds no second status or control for it.
export function RequirePermission({
  anyOf,
  children,
}: RequirePermissionProps): ReactNode {
  const session = useSession();
  const location = useLocation();
  const from = `${location.pathname}${location.search}${location.hash}`;
  useRedirectOnce(session.data === null, "/login", {
    replace: true,
    state: { from },
  });
  if (session.data === undefined) {
    return session.isError ? (
      <p>This page cannot be shown until your sign-in status is known.</p>
    ) : null;
  }
  if (session.data === null) {
    return null;
  }
  if (!hasAnyPermission(session.data.permissions, anyOf)) {
    return <AccessDeniedRoute />;
  }
  return children;
}
