import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { NavIcon, type IconName } from "../app/nav-icons.js";
import type { KnownPermission } from "../features/auth/api/auth-schemas.js";
import { PermissionGate } from "../features/auth/components/permission-gate.js";
import { permissions } from "../features/auth/permissions.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

type Section = Readonly<{
  to: string;
  label: string;
  summary: string;
  icon: IconName;
  anyOf: readonly KnownPermission[];
}>;

const SECTIONS: readonly Section[] = [
  {
    to: "/admin/media-servers",
    label: "Media servers",
    summary: "Connect the Jellyfin servers Bloom manages.",
    icon: "server",
    anyOf: [permissions.adminSettings],
  },
  {
    to: "/admin/invites",
    label: "Invites",
    summary: "Create links that let people join a media server.",
    icon: "ticket",
    anyOf: [permissions.usersInvite],
  },
  {
    to: "/admin/requests",
    label: "Requests",
    summary: "Approve or decline what people asked for.",
    icon: "inbox",
    anyOf: [permissions.requestsApprove],
  },
  {
    to: "/admin/request-profiles",
    label: "Request settings",
    summary: "Set the TMDB key and where approved requests go.",
    icon: "sliders",
    anyOf: [permissions.adminSettings],
  },
  {
    to: "/admin/download-managers",
    label: "Download managers",
    summary: "Connect the Radarr and Sonarr instances that fetch requests.",
    icon: "download",
    anyOf: [permissions.adminSettings],
  },
  {
    to: "/admin/notifications",
    label: "Notifications",
    summary: "Tell a webhook, Discord, or email about requests.",
    icon: "bell",
    anyOf: [permissions.adminSettings],
  },
  {
    to: "/admin/imports",
    label: "Imports",
    summary:
      "Bring in watch history from Playback Reporting or a Bloom export.",
    icon: "upload",
    anyOf: [permissions.adminSettings],
  },
  {
    to: "/admin/statistics",
    label: "Statistics",
    summary: "Most watched, most active, and when people watch.",
    icon: "chart",
    anyOf: [permissions.statsReadAll],
  },
  {
    to: "/admin/libraries",
    label: "Libraries",
    summary: "What each library holds, item by item, and who watched it.",
    icon: "library",
    anyOf: [permissions.statsReadAll],
  },
  {
    to: "/admin/playback",
    label: "Playback",
    summary: "See what is playing now and what finished recently.",
    icon: "play",
    anyOf: [permissions.statsReadAll],
  },
  {
    to: "/admin/accounts",
    label: "Accounts",
    summary: "Who has an account, their roles, and their media users.",
    icon: "users",
    anyOf: [permissions.usersManage],
  },
  {
    to: "/admin/roles",
    label: "Roles",
    summary: "See which permissions each role grants.",
    icon: "shield",
    anyOf: [permissions.adminRoles],
  },
];

export default function AdminRoute(): ReactNode {
  usePageTitle(pageTitle("Administration"));
  return (
    <>
      <h1>Administration</h1>
      <p className="page-intro">
        Everything an operator sets up and watches over, in one place.
      </p>
      <nav aria-label="Administration sections">
        <ul className="card-grid">
          {SECTIONS.map((section) => (
            <PermissionGate key={section.to} anyOf={section.anyOf}>
              <li className="card feature-tile">
                <span className="feature-tile-icon" aria-hidden="true">
                  <NavIcon name={section.icon} />
                </span>
                <h2>
                  <Link to={section.to}>{section.label}</Link>
                </h2>
                <p>{section.summary}</p>
              </li>
            </PermissionGate>
          ))}
        </ul>
      </nav>
    </>
  );
}
