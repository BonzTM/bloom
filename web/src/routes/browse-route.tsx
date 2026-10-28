import type { ReactNode } from "react";
import { Link, useParams } from "react-router-dom";
import { AsyncStatus } from "../components/async-status.js";
import {
  useSession,
  useSessionRecheck,
} from "../features/auth/hooks/auth-queries.js";
import {
  discoverFilterIdSchema,
  discoverListSchema,
  filteredDiscoverListSchema,
  type DiscoverList,
  type DiscoverSource,
} from "../features/requests/api/metadata-schemas.js";
import { brandName } from "../features/requests/components/brands.js";
import { describeSearchError } from "../features/requests/components/request-errors.js";
import { TitleGrid } from "../features/requests/components/title-grid.js";
import {
  useDiscover,
  useGenres,
} from "../features/requests/hooks/requests-queries.js";
import { accessDenial } from "../lib/api/errors.js";
import { AccessDeniedRoute } from "./access-denied-route.js";
import { NotFoundRoute } from "./not-found-route.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

const LIST_HEADINGS: Readonly<Record<DiscoverList, string>> = {
  trending: "Trending this week",
  "movies/popular": "Popular movies",
  "movies/upcoming": "Upcoming movies",
  "series/popular": "Popular series",
  "series/upcoming": "Upcoming series",
};

// Every title of one home row, a grid at a time: "See all" on a row and
// each genre, studio, and network tile lead here.
export default function BrowseRoute(): ReactNode {
  const params = useParams();
  const source = readSource(params["*"] ?? "");
  const session = useSession();
  const accountId = session.data?.account.id;
  if (source === undefined) {
    return <NotFoundRoute />;
  }
  if (accountId === undefined) {
    return null;
  }
  return (
    <BrowsePage
      key={`${accountId}-${JSON.stringify(source)}`}
      accountId={accountId}
      source={source}
    />
  );
}

// The splat after /browse/: a curated list, or a filtered list and the
// canonical decimal identifier of its genre, studio, or network.
export function readSource(splat: string): DiscoverSource | undefined {
  const fixed = discoverListSchema.safeParse(splat);
  if (fixed.success) {
    return fixed.data;
  }
  const match = /^([a-z]+\/[a-z]+)\/([1-9][0-9]{0,9})$/.exec(splat);
  const list = filteredDiscoverListSchema.safeParse(match?.[1]);
  const id = discoverFilterIdSchema.safeParse(Number(match?.[2]));
  return list.success && id.success
    ? { list: list.data, id: id.data }
    : undefined;
}

function BrowsePage({
  accountId,
  source,
}: Readonly<{ accountId: string; source: DiscoverSource }>): ReactNode {
  const query = useDiscover(accountId, source, true);
  const heading = useHeading(accountId, source);
  usePageTitle(pageTitle(heading));
  const denial = accessDenial(query.error);
  useSessionRecheck(denial !== undefined, query.errorUpdatedAt);
  if (denial === "forbidden") {
    return <AccessDeniedRoute />;
  }
  return (
    <>
      <p>
        <Link to="/">Back to home</Link>
      </p>
      <h1>{heading}</h1>
      <BrowseResults heading={heading} query={query} />
    </>
  );
}

function BrowseResults({
  heading,
  query,
}: Readonly<{
  heading: string;
  query: ReturnType<typeof useDiscover>;
}>): ReactNode {
  if (query.status === "pending") {
    return <AsyncStatus>Loading {heading.toLowerCase()}…</AsyncStatus>;
  }
  if (query.data === undefined) {
    return (
      <AsyncStatus kind="alert">{describeSearchError(query.error)}</AsyncStatus>
    );
  }
  const titles = query.data.pages.flatMap((page) => page.items);
  if (titles.length === 0) {
    return <p>Nothing to show right now.</p>;
  }
  return (
    <>
      <TitleGrid label={heading} titles={titles} />
      {query.hasNextPage ? (
        <p className="browse-more">
          <button
            type="button"
            disabled={query.isFetchingNextPage}
            onClick={() => {
              void query.fetchNextPage();
            }}
          >
            {query.isFetchingNextPage ? "Loading…" : "Load more"}
          </button>
        </p>
      ) : null}
      {query.isFetchNextPageError ? (
        <p role="alert">More {heading.toLowerCase()} could not be loaded.</p>
      ) : null}
    </>
  );
}

// A genre is named from TMDB's genre list, a studio or network from the
// tiles; an unknown one falls back to what it is.
function useHeading(accountId: string, source: DiscoverSource): string {
  const genreKind =
    typeof source !== "string" && source.list.endsWith("/genres")
      ? source.list === "movies/genres"
        ? "movie"
        : "series"
      : undefined;
  const genres = useGenres(
    accountId,
    genreKind ?? "movie",
    genreKind !== undefined,
  );
  if (typeof source === "string") {
    return LIST_HEADINGS[source];
  }
  if (genreKind !== undefined) {
    const name = genres.data?.find((genre) => genre.id === source.id)?.name;
    const noun = genreKind === "movie" ? "movies" : "series";
    return name === undefined ? `Genre ${noun}` : `${name} ${noun}`;
  }
  const name = brandName(source.list, source.id);
  if (source.list === "movies/studios") {
    return name === undefined ? "Studio movies" : `${name} movies`;
  }
  return name === undefined ? "Network series" : `${name} series`;
}
