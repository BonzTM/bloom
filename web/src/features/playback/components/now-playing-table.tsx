import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { AsyncStatus } from "../../../components/async-status.js";
import type { Watch } from "../api/playback-schemas.js";
import type { useNowPlaying } from "../hooks/playback-queries.js";
import {
  formatActiveTime,
  formatPosition,
  itemTitle,
  playMethodLabel,
  whereLabel,
  streamSummary,
  watchPath,
} from "./watch-format.js";

type NowPlayingQuery = ReturnType<typeof useNowPlaying>;

// The route owns the query and decides what a 401 or 403 means; this renders
// loading, empty, rows, a failed refresh that keeps the rows, and a failed
// first load.
export function NowPlayingTable({
  query,
}: Readonly<{ query: NowPlayingQuery }>): ReactNode {
  if (query.status === "pending") {
    return <AsyncStatus>Loading what is playing…</AsyncStatus>;
  }
  if (query.status === "error" && query.data === undefined) {
    return (
      <>
        <AsyncStatus kind="alert">
          What is playing could not be loaded.
        </AsyncStatus>
        <RetryButton retrying={false} onRetry={query.refetch} />
      </>
    );
  }
  const items = query.data.items;
  return (
    <>
      {query.isError ? (
        <div className="stale-warning">
          <AsyncStatus kind="alert">
            What is playing could not be refreshed. What is shown may be out of
            date.
          </AsyncStatus>
          <RetryButton retrying={query.isRefetching} onRetry={query.refetch} />
        </div>
      ) : null}
      {items.length === 0 ? (
        <p>Nothing is playing right now.</p>
      ) : (
        <Table watches={items} />
      )}
    </>
  );
}

function Table({
  watches,
}: Readonly<{ watches: readonly Watch[] }>): ReactNode {
  return (
    <div
      className="table-scroll"
      role="region"
      aria-label="Playing now table"
      tabIndex={0}
    >
      <table className="playback-table">
        <caption>Playing now, newest first</caption>
        <thead>
          <tr>
            <th scope="col">User</th>
            <th scope="col">Item</th>
            <th scope="col">Where</th>
            <th scope="col">Server</th>
            <th scope="col">Position</th>
            <th scope="col">Method</th>
            <th scope="col">Watched</th>
            <th scope="col">Stream</th>
            <th scope="col">Details</th>
          </tr>
        </thead>
        <tbody>
          {watches.map((watch) => (
            <tr key={watch.id}>
              <th scope="row">{watch.username || watch.media_user_id}</th>
              <td>
                {itemTitle(watch)}
                {watch.item_type === "" ? null : (
                  <span className="item-type"> {watch.item_type}</span>
                )}
              </td>
              <td>{whereLabel(watch)}</td>
              <td>{watch.media_server_name}</td>
              <td>
                <time>{formatPosition(watch.position_ms)}</time>
                {watch.paused ? (
                  <span className="badge badge-neutral"> paused</span>
                ) : null}
              </td>
              <td>{playMethodLabel(watch.play_method)}</td>
              <td>{formatActiveTime(watch.active_seconds)}</td>
              <td>{streamSummary(watch.stream)}</td>
              <td>
                <Link to={watchPath(watch.id)}>
                  Details
                  <span className="visually-hidden">
                    {" "}
                    of {itemTitle(watch)}
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
