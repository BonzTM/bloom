import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { AsyncStatus } from "../../../components/async-status.js";
import {
  LoadFailed,
  Pager,
  RefreshFailed,
} from "../../requests/components/list-states.js";
import type { Watch } from "../api/playback-schemas.js";
import type { useActivity } from "../hooks/playback-queries.js";
import {
  formatActiveTime,
  itemTitle,
  playMethodLabel,
  watchPath,
  whereLabel,
} from "./watch-format.js";

type ActivityQuery = ReturnType<typeof useActivity>;

// Every watch that matches the page's filter, newest first. The route owns
// the query and decides what a 401 or 403 means; this renders every other
// state and links each person to their statistics and each watch to its
// details.
export function ActivityTable({
  query,
}: Readonly<{ query: ActivityQuery }>): ReactNode {
  if (query.status === "pending") {
    return <AsyncStatus>Loading activity…</AsyncStatus>;
  }
  if (query.status === "error" && query.data === undefined) {
    return <LoadFailed noun="activity" onRetry={query.refetch} />;
  }
  const items = query.data.pages.flatMap((page) => page.items);
  const refreshFailed = query.isError && !query.isFetchNextPageError;
  return (
    <>
      {refreshFailed ? (
        <RefreshFailed
          noun="activity"
          retrying={query.isRefetching}
          onRetry={query.refetch}
        />
      ) : null}
      {items.length === 0 ? (
        <p role="status">Nothing matches these filters.</p>
      ) : (
        <Table watches={items} />
      )}
      <Pager
        noun="activity"
        hasMore={query.hasNextPage}
        loading={query.isFetchingNextPage}
        failed={query.isFetchNextPageError}
        onMore={query.fetchNextPage}
      />
    </>
  );
}

const SOURCE_LABELS: Readonly<Record<string, string>> = {
  poll: "Polled",
  websocket: "Live",
  webhook: "Webhook",
  import: "Imported",
};

export function sourceLabel(source: string): string {
  return SOURCE_LABELS[source] ?? source;
}

function personPath(watch: Watch): string {
  return `/admin/statistics/users/${encodeURIComponent(watch.media_server_id)}/${encodeURIComponent(watch.media_user_id)}`;
}

function Table({
  watches,
}: Readonly<{ watches: readonly Watch[] }>): ReactNode {
  return (
    <div
      className="table-scroll"
      role="region"
      aria-label="Activity table"
      tabIndex={0}
    >
      <table className="activity-table">
        <caption>Watches matching the filters, newest first</caption>
        <thead>
          <tr>
            <th scope="col">Started</th>
            <th scope="col">Person</th>
            <th scope="col">Item</th>
            <th scope="col">Library</th>
            <th scope="col">Server</th>
            <th scope="col">Where</th>
            <th scope="col">Watched</th>
            <th scope="col">Method</th>
            <th scope="col">Source</th>
            <th scope="col">Details</th>
          </tr>
        </thead>
        <tbody>
          {watches.map((watch) => (
            <tr key={watch.id}>
              <td>
                <time dateTime={watch.started_at}>
                  {watch.started_at.slice(0, 16).replace("T", " ")}
                </time>
              </td>
              <th scope="row">
                <Link to={personPath(watch)}>
                  {watch.username || watch.media_user_id}
                </Link>
              </th>
              <td>{itemTitle(watch)}</td>
              <td>{watch.library_name}</td>
              <td>{watch.media_server_name}</td>
              <td>{whereLabel(watch)}</td>
              <td>{formatActiveTime(watch.active_seconds)}</td>
              <td>{playMethodLabel(watch.play_method)}</td>
              <td>{sourceLabel(watch.source)}</td>
              <td>
                <Link to={watchPath(watch.id)}>
                  Details
                  <span className="visually-hidden">
                    {" "}
                    of {itemTitle(watch)} by{" "}
                    {watch.username || watch.media_user_id}
                  </span>
                </Link>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
