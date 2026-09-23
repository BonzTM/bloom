import type { ReactNode } from "react";
import { AsyncStatus } from "../../../components/async-status.js";
import type { HistoryWatch } from "../api/playback-schemas.js";
import type { usePlaybackHistory } from "../hooks/playback-queries.js";
import {
  formatActiveTime,
  itemTitle,
  playMethodLabel,
  whereLabel,
} from "./watch-format.js";

type HistoryQuery = ReturnType<typeof usePlaybackHistory>;

// The route owns the query and decides what a 401 or 403 means; this renders
// every other state: loading, empty, rows, a failed refresh that keeps the
// rows, a failed later page that keeps the rows, and a failed first load.
export function HistoryTable({
  query,
}: Readonly<{ query: HistoryQuery }>): ReactNode {
  if (query.status === "pending") {
    return <AsyncStatus>Loading history…</AsyncStatus>;
  }
  if (query.status === "error" && query.data === undefined) {
    return (
      <>
        <AsyncStatus kind="alert">The history could not be loaded.</AsyncStatus>
        <RetryButton retrying={false} onRetry={query.refetch} />
      </>
    );
  }
  const items = query.data.pages.flatMap((page) => page.items);
  const refreshFailed = query.isError && !query.isFetchNextPageError;
  return (
    <>
      {refreshFailed ? (
        <div className="stale-warning">
          <AsyncStatus kind="alert">
            The history could not be refreshed. What is shown may be out of
            date.
          </AsyncStatus>
          <RetryButton retrying={query.isRefetching} onRetry={query.refetch} />
        </div>
      ) : null}
      {items.length === 0 ? (
        <p>No finished watches have been recorded yet.</p>
      ) : (
        <Table watches={items} />
      )}
      <Pager
        hasMore={query.hasNextPage}
        loading={query.isFetchingNextPage}
        failed={query.isFetchNextPageError}
        onMore={query.fetchNextPage}
      />
    </>
  );
}

function Table({
  watches,
}: Readonly<{ watches: readonly HistoryWatch[] }>): ReactNode {
  return (
    <div
      className="table-scroll"
      role="region"
      aria-label="Finished watches table"
      tabIndex={0}
    >
      <table className="playback-table">
        <caption>Finished watches, newest first</caption>
        <thead>
          <tr>
            <th scope="col">User</th>
            <th scope="col">Item</th>
            <th scope="col">Where</th>
            <th scope="col">Server</th>
            <th scope="col">Started</th>
            <th scope="col">Watched</th>
            <th scope="col">Method</th>
          </tr>
        </thead>
        <tbody>
          {watches.map((watch) => (
            <tr key={watch.id}>
              <th scope="row">{watch.username || watch.media_user_id}</th>
              <td>{itemTitle(watch)}</td>
              <td>{whereLabel(watch)}</td>
              <td>{watch.media_server_name}</td>
              <td>
                <time dateTime={watch.started_at}>
                  {watch.started_at.slice(0, 16).replace("T", " ")}
                </time>
              </td>
              <td>{formatActiveTime(watch.active_seconds)}</td>
              <td>{playMethodLabel(watch.play_method)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

type PagerProps = Readonly<{
  hasMore: boolean;
  loading: boolean;
  failed: boolean;
  onMore: () => Promise<unknown>;
}>;

function Pager({ hasMore, loading, failed, onMore }: PagerProps): ReactNode {
  if (!hasMore) {
    return null;
  }
  return (
    <div className="pager">
      {failed ? <p role="alert">More history could not be loaded.</p> : null}
      <button
        type="button"
        disabled={loading}
        onClick={() => {
          void onMore();
        }}
      >
        {loading ? "Loading more…" : "Load more history"}
      </button>
      <span role="status">{loading ? "Loading more history." : ""}</span>
    </div>
  );
}

function RetryButton({
  retrying,
  onRetry,
}: Readonly<{
  retrying: boolean;
  onRetry: () => Promise<unknown>;
}>): ReactNode {
  return (
    <button
      type="button"
      disabled={retrying}
      onClick={() => {
        void onRetry();
      }}
    >
      {retrying ? "Retrying…" : "Retry"}
    </button>
  );
}
