import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { NavIcon, type IconName } from "../app/nav-icons.js";
import { PermissionGate } from "../features/auth/components/permission-gate.js";
import {
  useSession,
  useSessionRecheck,
} from "../features/auth/hooks/auth-queries.js";
import type { KnownPermission } from "../features/auth/api/auth-schemas.js";
import {
  ADMIN_PERMISSIONS,
  permissions,
  REQUESTS_PERMISSIONS,
} from "../features/auth/permissions.js";
import { NowPlaying } from "../features/playback/components/now-playing.js";
import { useNowPlaying } from "../features/playback/hooks/playback-queries.js";
import { DiscoverRow } from "../features/requests/components/discover-row.js";
import { MyRequests } from "../features/requests/components/my-requests.js";
import {
  useDiscover,
  useRequests,
} from "../features/requests/hooks/requests-queries.js";
import { VersionBadge } from "../features/system/components/version-badge.js";
import { accessDenial } from "../lib/api/errors.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

// Signed out, the home page introduces the three areas of the app. Signed
// in, it is a dashboard: what is playing, what is trending, the account's
// own requests, and quick links into each area the account may use.
export function HomeRoute(): ReactNode {
  usePageTitle(pageTitle("Home"));
  const session = useSession();
  const accountId = session.data?.account.id;
  return (
    <>
      <h1>Bloom</h1>
      {accountId === undefined ? (
        <Landing />
      ) : (
        <Dashboard key={accountId} accountId={accountId} />
      )}
      <VersionBadge />
    </>
  );
}

type Area = Readonly<{
  id: string;
  heading: string;
  summary: string;
  icon: IconName;
  to: string;
  action: string;
}>;

const areas: readonly Area[] = [
  {
    id: "users",
    heading: "Users & invites",
    summary:
      "Create invite links, manage media-server accounts, and assign permissions and libraries.",
    icon: "ticket",
    to: "/admin/invites",
    action: "Open invites",
  },
  {
    id: "statistics",
    heading: "Statistics",
    summary:
      "See who is watching now, browse playback history, and explore per-user and per-library dashboards.",
    icon: "chart",
    to: "/admin/statistics",
    action: "Open statistics",
  },
  {
    id: "requests",
    heading: "Requests",
    summary:
      "Discover movies and series, request them with quotas and approval rules, and follow availability.",
    icon: "compass",
    to: "/requests",
    action: "Open requests",
  },
];

function Landing(): ReactNode {
  return (
    <>
      <p className="page-intro">
        Invites, statistics, and requests for your Jellyfin servers, in one
        place.
      </p>
      <div className="card-grid">
        {areas.map((area) => (
          <AreaCard key={area.id} area={area} />
        ))}
      </div>
    </>
  );
}

function AreaCard({ area }: Readonly<{ area: Area }>): ReactNode {
  const headingId = `${area.id}-heading`;
  return (
    <section aria-labelledby={headingId} className="card feature-tile">
      <span className="feature-tile-icon" aria-hidden="true">
        <NavIcon name={area.icon} />
      </span>
      <h2 id={headingId}>{area.heading}</h2>
      <p>{area.summary}</p>
      <p>
        <Link to={area.to}>{area.action}</Link>
      </p>
    </section>
  );
}

function Dashboard({ accountId }: Readonly<{ accountId: string }>): ReactNode {
  return (
    <div className="dashboard">
      <p className="page-intro">
        What is playing, what is trending, and where your requests stand.
      </p>
      <PermissionGate anyOf={[permissions.statsReadAll]}>
        <NowPlayingPanel accountId={accountId} />
      </PermissionGate>
      <PermissionGate anyOf={REQUESTS_PERMISSIONS}>
        <TrendingPanel accountId={accountId} />
      </PermissionGate>
      <PermissionGate anyOf={[permissions.requestsReadOwn]}>
        <MyRequestsPanel accountId={accountId} />
      </PermissionGate>
      <PermissionGate anyOf={[...ADMIN_PERMISSIONS, ...REQUESTS_PERMISSIONS]}>
        <QuickLinks />
      </PermissionGate>
    </div>
  );
}

// Each panel owns its query so the request is only made when the gate
// around it rendered; a 401 asks the session to be checked again.
function NowPlayingPanel({
  accountId,
}: Readonly<{ accountId: string }>): ReactNode {
  const query = useNowPlaying(accountId);
  useSessionRecheck(
    accessDenial(query.error) === "unauthenticated",
    query.errorUpdatedAt,
  );
  return (
    <section aria-labelledby="home-playing-heading" className="card">
      <div className="section-head">
        <h2 id="home-playing-heading">Playing now</h2>
        <Link to="/admin/playback">All playback</Link>
      </div>
      <NowPlaying query={query} />
    </section>
  );
}

function TrendingPanel({
  accountId,
}: Readonly<{ accountId: string }>): ReactNode {
  const query = useDiscover(accountId, "trending", true);
  useSessionRecheck(
    accessDenial(query.error) === "unauthenticated",
    query.errorUpdatedAt,
  );
  return (
    <section aria-labelledby="home-trending-heading" className="card">
      <div className="section-head">
        <h2 id="home-trending-heading">Trending this week</h2>
        <Link to="/requests">Discover more</Link>
      </div>
      <DiscoverRow title="Trending this week" query={query} bare />
    </section>
  );
}

function MyRequestsPanel({
  accountId,
}: Readonly<{ accountId: string }>): ReactNode {
  const query = useRequests(accountId, { requesterId: accountId });
  useSessionRecheck(
    accessDenial(query.error) === "unauthenticated",
    query.errorUpdatedAt,
  );
  return (
    <section aria-labelledby="home-requests-heading" className="card">
      <div className="section-head">
        <h2 id="home-requests-heading">My requests</h2>
        <Link to="/requests">Request a title</Link>
      </div>
      <MyRequests query={query} accountId={accountId} />
    </section>
  );
}

type QuickLink = Readonly<{
  to: string;
  label: string;
  icon: IconName;
  anyOf: readonly KnownPermission[];
}>;

// The link names match the landing page so either entry reads the same.
const QUICK_LINKS: readonly QuickLink[] = [
  {
    to: "/requests",
    label: "Open requests",
    icon: "compass",
    anyOf: REQUESTS_PERMISSIONS,
  },
  {
    to: "/admin/playback",
    label: "Open playback",
    icon: "play",
    anyOf: [permissions.statsReadAll],
  },
  {
    to: "/admin/statistics",
    label: "Open statistics",
    icon: "chart",
    anyOf: [permissions.statsReadAll],
  },
  {
    to: "/admin/libraries",
    label: "Open libraries",
    icon: "library",
    anyOf: [permissions.statsReadAll],
  },
  {
    to: "/admin/invites",
    label: "Open invites",
    icon: "ticket",
    anyOf: [permissions.usersInvite],
  },
  {
    to: "/admin/accounts",
    label: "Open accounts",
    icon: "users",
    anyOf: [permissions.usersManage],
  },
  {
    to: "/admin/media-servers",
    label: "Open media servers",
    icon: "server",
    anyOf: [permissions.adminSettings],
  },
  {
    to: "/admin",
    label: "Open administration",
    icon: "shield",
    anyOf: ADMIN_PERMISSIONS,
  },
];

function QuickLinks(): ReactNode {
  return (
    <nav aria-labelledby="home-links-heading" className="card">
      <div className="section-head">
        <h2 id="home-links-heading">Go to</h2>
      </div>
      <ul className="quick-links">
        {QUICK_LINKS.map((link) => (
          <PermissionGate key={link.to} anyOf={link.anyOf}>
            <li>
              <Link to={link.to} className="quick-link">
                <NavIcon name={link.icon} />
                {link.label}
              </Link>
            </li>
          </PermissionGate>
        ))}
      </ul>
    </nav>
  );
}
