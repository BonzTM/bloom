import { useId, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { AsyncStatus } from "../../../components/async-status.js";
import type { MetadataDiscoverTitle } from "../api/metadata-schemas.js";
import type { useDiscover } from "../hooks/requests-queries.js";
import { Poster } from "./poster.js";
import { describeSearchError } from "./request-errors.js";
import {
  kindLabel,
  requestStateBadge,
  titleWithYear,
} from "./request-format.js";
import { titlePath } from "./title-grid.js";

type DiscoverRowProps = Readonly<{
  title: string;
  query: ReturnType<typeof useDiscover>;
  // A bare row has no heading of its own: the caller's section names it.
  bare?: boolean;
}>;

// One horizontal row of posters, the way Seerr lays out its discover page.
// The row is a region named by its heading; the list inside scrolls
// sideways and grows a page at a time.
export function DiscoverRow({
  title,
  query,
  bare,
}: DiscoverRowProps): ReactNode {
  const id = useId();
  if (bare === true) {
    return <RowBody title={title} query={query} />;
  }
  return (
    <section aria-labelledby={id} className="discover-row">
      <h2 id={id}>{title}</h2>
      <RowBody title={title} query={query} />
    </section>
  );
}

function RowBody({ title, query }: DiscoverRowProps): ReactNode {
  if (query.status === "pending") {
    return <AsyncStatus>Loading {title.toLowerCase()}…</AsyncStatus>;
  }
  if (query.status === "error" && query.data === undefined) {
    return (
      <>
        <AsyncStatus kind="alert">
          {describeSearchError(query.error)}
        </AsyncStatus>
        <button
          type="button"
          onClick={() => {
            void query.refetch();
          }}
        >
          Retry
        </button>
      </>
    );
  }
  const titles = query.data.pages.flatMap((page) => page.items);
  if (titles.length === 0) {
    return <p className="chart-empty">Nothing to show right now.</p>;
  }
  return (
    <div className="discover-scroller">
      <ul className="discover-list" aria-label={title}>
        {titles.map((item) => (
          <li key={`${item.kind}-${item.provider_id}`}>
            <DiscoverCard item={item} />
          </li>
        ))}
        {query.hasNextPage ? (
          <li className="discover-more">
            <button
              type="button"
              disabled={query.isFetchingNextPage}
              onClick={() => {
                void query.fetchNextPage();
              }}
            >
              {query.isFetchingNextPage ? "Loading…" : "More"}
              <span className="visually-hidden"> {title.toLowerCase()}</span>
            </button>
          </li>
        ) : null}
      </ul>
      {query.isFetchNextPageError ? (
        <p role="alert">More {title.toLowerCase()} could not be loaded.</p>
      ) : null}
    </div>
  );
}

// One poster card, the way Seerr draws it: the kind in the top corner, the
// caller's own request state in the other, and a check once it is available.
export function DiscoverCard({
  item,
}: Readonly<{ item: MetadataDiscoverTitle }>): ReactNode {
  return (
    <Link to={titlePath(item)} className="poster-card discover-card">
      <span className="discover-card-art">
        <Poster posterPath={item.poster_path} title={item.title} size="w342" />
        <span className="discover-card-kind" aria-hidden="true">
          {kindLabel(item.kind)}
        </span>
        <RequestStateMark state={item.request_state} />
      </span>
      <span className="poster-card-title">{titleWithYear(item)}</span>
      <span className="poster-card-kind">{kindLabel(item.kind)}</span>
    </Link>
  );
}

function RequestStateMark({
  state,
}: Readonly<{ state: MetadataDiscoverTitle["request_state"] }>): ReactNode {
  const badge = requestStateBadge(state);
  if (badge === undefined) {
    return null;
  }
  if (state === "available") {
    return (
      <span className="discover-card-state discover-card-available">
        <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false">
          <path d="m6 12.5 4 4 8-9" />
        </svg>
        <span className="visually-hidden">{badge.label}</span>
      </span>
    );
  }
  return (
    <span className={`${badge.className} discover-card-state`}>
      {badge.label}
    </span>
  );
}
