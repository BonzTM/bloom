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
        <ul className="card-grid">
          <PermissionGate anyOf={[permissions.adminSettings]}>
            <li className="card">
              <h2>
                <Link to="/admin/media-servers">Media servers</Link>
              </h2>
              <p>Connect the Jellyfin servers Bloom manages.</p>
            </li>
          </PermissionGate>
          <PermissionGate anyOf={[permissions.usersInvite]}>
            <li className="card">
              <h2>
                <Link to="/admin/invites">Invites</Link>
              </h2>
              <p>Create links that let people join a media server.</p>
            </li>
          </PermissionGate>
          <PermissionGate anyOf={[permissions.requestsApprove]}>
            <li className="card">
              <h2>
                <Link to="/admin/requests">Requests</Link>
              </h2>
              <p>Approve or decline what people asked for.</p>
            </li>
          </PermissionGate>
          <PermissionGate anyOf={[permissions.adminSettings]}>
            <li className="card">
              <h2>
                <Link to="/admin/request-profiles">Request settings</Link>
              </h2>
              <p>Set the TMDB key and where approved requests go.</p>
            </li>
          </PermissionGate>
          <PermissionGate anyOf={[permissions.adminSettings]}>
            <li className="card">
              <h2>
                <Link to="/admin/download-managers">Download managers</Link>
              </h2>
              <p>
                Connect the Radarr and Sonarr instances that fetch requests.
              </p>
            </li>
          </PermissionGate>
          <PermissionGate anyOf={[permissions.statsReadAll]}>
            <li className="card">
              <h2>
                <Link to="/admin/playback">Playback</Link>
              </h2>
              <p>See what is playing now and what finished recently.</p>
            </li>
          </PermissionGate>
          <PermissionGate anyOf={[permissions.adminRoles]}>
            <li className="card">
              <h2>
                <Link to="/admin/roles">Roles</Link>
              </h2>
              <p>See which permissions each role grants.</p>
            </li>
          </PermissionGate>
        </ul>
      </nav>
    </>
  );
}
