import type { ReactNode } from "react";
import { AsyncStatus } from "../../../components/async-status.js";
import { MediaRow } from "../../../components/media-row.js";
import type { useDiscover } from "../hooks/requests-queries.js";
import { PosterRow } from "./poster-row.js";
import { describeSearchError } from "./request-errors.js";

type DiscoverRowProps = Readonly<{
  title: string;
  query: ReturnType<typeof useDiscover>;
  seeAll?: string | undefined;
}>;

// One discover row: its loading and failure states, then posters that grow
// a page at a time from a "More" card at the end.
export function DiscoverRow({
  title,
  query,
  seeAll,
}: DiscoverRowProps): ReactNode {
  if (query.status === "pending") {
    return (
      <MediaRow
        title={title}
        status={<AsyncStatus>Loading {title.toLowerCase()}…</AsyncStatus>}
      />
    );
  }
  if (query.status === "error" && query.data === undefined) {
    return (
      <MediaRow
        title={title}
        status={
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
        }
      />
    );
  }
  const titles = query.data.pages.flatMap((page) => page.items);
  return (
    <PosterRow
      title={title}
      titles={titles}
      seeAll={seeAll}
      trailing={
        query.hasNextPage ? <MoreCard title={title} query={query} /> : null
      }
      footer={
        query.isFetchNextPageError ? (
          <p role="alert">More {title.toLowerCase()} could not be loaded.</p>
        ) : null
      }
    />
  );
}

function MoreCard({
  title,
  query,
}: Readonly<{
  title: string;
  query: ReturnType<typeof useDiscover>;
}>): ReactNode {
  return (
    <li className="media-row-more">
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
  );
}
