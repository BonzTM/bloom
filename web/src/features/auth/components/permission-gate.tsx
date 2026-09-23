import type { ReactNode } from "react";
import type { KnownPermission } from "../api/auth-schemas.js";
import { useSession } from "../hooks/auth-queries.js";
import { hasAnyPermission } from "../permissions.js";

type PermissionGateProps = Readonly<{
  anyOf: readonly KnownPermission[];
  children: ReactNode;
}>;

// Shows its children only to a signed-in account holding at least one of the
// permissions. It hides, it does not protect: the server checks every request,
// so this only keeps controls the person cannot use off the screen.
export function PermissionGate({
  anyOf,
  children,
}: PermissionGateProps): ReactNode {
  const session = useSession();
  const granted = session.data?.permissions ?? [];
  return hasAnyPermission(granted, anyOf) ? children : null;
}
