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
import {
  RecentlyAddedRow,
  type RecentlyAddedAccess,
} from "../features/catalog/components/recently-added-row.js";
import { useRecentlyAdded } from "../features/catalog/hooks/catalog-queries.js";
import { browsePath } from "../features/requests/components/browse-path.js";
import { NETWORKS, STUDIOS } from "../features/requests/components/brands.js";
import { DiscoverRow } from "../features/requests/components/discover-row.js";
import { MyRequests } from "../features/requests/components/my-requests.js";
import {
  BrandRow,
  GenreRow,
} from "../features/requests/components/tile-rows.js";
import {
  useDiscover,
  useGenres,
  useRequests,
} from "../features/requests/hooks/requests-queries.js";
import { accessDenial } from "../lib/api/errors.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

// Signed out, the home page introduces Bloom. Signed in, it works like
// Seerr's discover page: what is playing, what the library just added, rows
// of titles to request, and the account's own requests. The search box and
// the sidebar are the way into everything else.
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
  const canRequest = hasPermission(granted, permissions.requestsCreate);
  return (
    <div className="dashboard">
      <p className="page-intro">
        What is playing, what just arrived, and what to watch or request next.
      </p>
      <PermissionGate anyOf={[permissions.statsReadAll]}>
        <NowPlayingPanel accountId={accountId} />
      </PermissionGate>
      <PermissionGate
        anyOf={[permissions.requestsCreate, permissions.requestsReadOwn]}
      >
        <RecentlyAddedPanel
          accountId={accountId}
          access={recentlyAddedAccess(granted)}
        />
      </PermissionGate>
      {/* Posters open a title page, which needs requests.create. */}
      <PermissionGate anyOf={[permissions.requestsCreate]}>
        <DiscoverRows accountId={accountId} />
      </PermissionGate>
      <PermissionGate anyOf={[permissions.requestsReadOwn]}>
        <MyRequestsPanel accountId={accountId} canRequest={canRequest} />
      </PermissionGate>
    </div>
  );
}

function recentlyAddedAccess(
  granted: readonly Permission[],
): RecentlyAddedAccess {
  return {
    titles: hasPermission(granted, permissions.requestsCreate),
    items: hasPermission(granted, permissions.statsReadAll),
  };
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

function RecentlyAddedPanel({
  accountId,
  access,
}: Readonly<{ accountId: string; access: RecentlyAddedAccess }>): ReactNode {
  const query = useRecentlyAdded(accountId, true);
  useSessionRecheck(
    accessDenial(query.error) !== undefined,
    query.errorUpdatedAt,
  );
  return <RecentlyAddedRow query={query} access={access} />;
}

// The discovery rows in Seerr's order: what is trending, then movies and
// series, each with its popular, genre, upcoming, and studio or network
// rows.
function DiscoverRows({
  accountId,
}: Readonly<{ accountId: string }>): ReactNode {
  const trending = useDiscover(accountId, "trending", true);
  const popularMovies = useDiscover(accountId, "movies/popular", true);
  const upcomingMovies = useDiscover(accountId, "movies/upcoming", true);
  const popularSeries = useDiscover(accountId, "series/popular", true);
  const upcomingSeries = useDiscover(accountId, "series/upcoming", true);
  const movieGenres = useGenres(accountId, "movie", true);
  const seriesGenres = useGenres(accountId, "series", true);
  const queries = [trending, popularMovies, movieGenres] as const;
  useSessionRecheck(
    queries.some((query) => accessDenial(query.error) !== undefined),
    Math.max(...queries.map((query) => query.errorUpdatedAt)),
  );
  return (
    <>
      <DiscoverRow
        title="Trending"
        query={trending}
        seeAll={browsePath("trending")}
      />
      <DiscoverRow
        title="Popular movies"
        query={popularMovies}
        seeAll={browsePath("movies/popular")}
      />
      <GenreRow title="Movie genres" kind="movie" query={movieGenres} />
      <DiscoverRow
        title="Upcoming movies"
        query={upcomingMovies}
        seeAll={browsePath("movies/upcoming")}
      />
      <BrandRow title="Studios" brands={STUDIOS} list="studios" />
      <DiscoverRow
        title="Popular series"
        query={popularSeries}
        seeAll={browsePath("series/popular")}
      />
      <GenreRow title="Series genres" kind="series" query={seriesGenres} />
      <DiscoverRow
        title="Upcoming series"
        query={upcomingSeries}
        seeAll={browsePath("series/upcoming")}
      />
      <BrandRow title="Networks" brands={NETWORKS} list="networks" />
    </>
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
        {canRequest ? <Link to="/search">Search for a title</Link> : null}
      </div>
      <MyRequests query={query} accountId={accountId} linkTitles={canRequest} />
    </section>
  );
}
