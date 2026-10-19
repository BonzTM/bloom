import type { ReactNode } from "react";
import { Navigate } from "react-router-dom";
import { firstAdminPage } from "../app/nav-model.js";
import { useSession } from "../features/auth/hooks/auth-queries.js";
import { AccessDeniedRoute } from "./access-denied-route.js";

// /admin has no page of its own: the sidebar lists every area. An old link
// or a typed address lands on the first administration page the account
// may open. The route guard above has already confirmed the session.
export default function AdminRoute(): ReactNode {
  const session = useSession();
  const granted = session.data?.permissions ?? [];
  const first = firstAdminPage(granted);
  if (first === undefined) {
    return <AccessDeniedRoute />;
  }
  return <Navigate to={first.to} replace />;
}
