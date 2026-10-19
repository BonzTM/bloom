import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { NavIcon } from "../app/nav-icons.js";
import { PermissionGate } from "../features/auth/components/permission-gate.js";
import {
  useSession,
  useSessionRecheck,
} from "../features/auth/hooks/auth-queries.js";
import type { Permission } from "../features/auth/api/auth-schemas.js";
import { hasPermission, permissions } from "../features/auth/permissions.js";
import { NowPlaying } from "../features/playback/components/now-playing.js";
import { useNowPlaying } from "../features/playback/hooks/playback-queries.js";
import { DiscoverRow } from "../features/requests/components/discover-row.js";
import { MyRequests } from "../features/requests/components/my-requests.js";
import {
  useDiscover,
  useRequests,
} from "../features/requests/hooks/requests-queries.js";
import { accessDenial } from "../lib/api/errors.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

// Signed out, the home page introduces Bloom. Signed in, it is a dashboard:
// what is playing, what is trending, and the account's own requests. The
// sidebar is the way into every other area.
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
        <Dashboard
          key={accountId}
          accountId={accountId}
          granted={session.data?.permissions ?? []}
        />
      )}
    </>
  );
}

// Signed out, the home page says what Bloom is and offers sign-in; every
// area needs an account, so there is nothing else to open yet.
function Landing(): ReactNode {
  return (
    <div className="landing">
      <p className="page-intro">
        Invites, statistics, and requests for your Jellyfin servers, in one
        place.
      </p>
      <ul className="landing-points">
        <li>
          <NavIcon name="ticket" />
          Invite people and manage their media-server accounts.
        </li>
        <li>
          <NavIcon name="chart" />
          See what is playing and what everyone watched.
        </li>
        <li>
          <NavIcon name="compass" />
          Discover movies and series and request them.
        </li>
      </ul>
      <p>
        <Link to="/login" className="btn btn-primary">
          Sign in to get started
        </Link>
      </p>
    </div>
  );
}

function Dashboard({
  accountId,
  granted,
}: Readonly<{
  accountId: string;
  granted: readonly Permission[];
}>): ReactNode {
  return (
    <div className="dashboard">
      <p className="page-intro">
        What is playing, what is trending, and where your requests stand.
      </p>
      <PermissionGate anyOf={[permissions.statsReadAll]}>
        <NowPlayingPanel accountId={accountId} />
      </PermissionGate>
      {/* Posters open a title page, which needs requests.create. */}
      <PermissionGate anyOf={[permissions.requestsCreate]}>
        <TrendingPanel accountId={accountId} />
      </PermissionGate>
      <PermissionGate anyOf={[permissions.requestsReadOwn]}>
        <MyRequestsPanel
          accountId={accountId}
          canRequest={hasPermission(granted, permissions.requestsCreate)}
        />
      </PermissionGate>
    </div>
  );
}

// Each panel owns its query so the request is only made when the gate
// around it rendered. A 401 or 403 asks the session to be checked again,
// so a signed-out or narrowed account loses the panel instead of keeping
// a retry control that can never succeed.
function NowPlayingPanel({
  accountId,
}: Readonly<{ accountId: string }>): ReactNode {
  const query = useNowPlaying(accountId);
  useSessionRecheck(
    accessDenial(query.error) !== undefined,
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
    accessDenial(query.error) !== undefined,
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
  canRequest,
}: Readonly<{ accountId: string; canRequest: boolean }>): ReactNode {
  const query = useRequests(accountId, { requesterId: accountId });
  useSessionRecheck(
    accessDenial(query.error) !== undefined,
    query.errorUpdatedAt,
  );
  return (
    <section aria-labelledby="home-requests-heading" className="card">
      <div className="section-head">
        <h2 id="home-requests-heading">My requests</h2>
        {canRequest ? <Link to="/requests">Request a title</Link> : null}
      </div>
      <MyRequests query={query} accountId={accountId} linkTitles={canRequest} />
    </section>
  );
}
