import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { PermissionGate } from "../features/auth/components/permission-gate.js";
import { permissions } from "../features/auth/permissions.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

export default function AdminRoute(): ReactNode {
  usePageTitle(pageTitle("Administration"));
  return (
    <>
      <h1>Administration</h1>
      <nav aria-label="Administration sections">
        <ul>
          <PermissionGate anyOf={[permissions.adminSettings]}>
            <li>
              <Link to="/admin/media-servers">Media servers</Link>
              <p>Connect the Jellyfin servers Bloom manages.</p>
            </li>
          </PermissionGate>
          <PermissionGate anyOf={[permissions.usersInvite]}>
            <li>
              <Link to="/admin/invites">Invites</Link>
              <p>Create links that let people join a media server.</p>
            </li>
          </PermissionGate>
          <PermissionGate anyOf={[permissions.adminRoles]}>
            <li>
              <Link to="/admin/roles">Roles</Link>
              <p>See which permissions each role grants.</p>
            </li>
          </PermissionGate>
        </ul>
      </nav>
    </>
  );
}
