import type { ReactNode } from "react";
import { AsyncStatus } from "../../../components/async-status.js";
import {
  formatActiveTime,
  playMethodLabel,
  whereLabel,
} from "../../playback/components/watch-format.js";
import { LoadFailed, Pager } from "../../requests/components/list-states.js";
import type { useItemHistory } from "../hooks/catalog-queries.js";

// Who watched this item (or anything under it), newest first.
export function ItemHistoryTable({
  query,
}: Readonly<{ query: ReturnType<typeof useItemHistory> }>): ReactNode {
  if (query.status === "pending") {
    return <AsyncStatus>Loading history…</AsyncStatus>;
  }
  if (query.status === "error" && query.data === undefined) {
    return <LoadFailed noun="history" onRetry={query.refetch} />;
  }
  const items = query.data.pages.flatMap((page) => page.items);
  if (items.length === 0) {
    return <p>Nobody has watched this yet.</p>;
  }
  return (
    <>
      <div
        className="table-scroll"
        role="region"
        aria-label="Item history table"
        tabIndex={0}
      >
        <table>
          <caption>Watches, newest first</caption>
          <thead>
            <tr>
              <th scope="col">Person</th>
              <th scope="col">Episode</th>
              <th scope="col">Where</th>
              <th scope="col">Method</th>
              <th scope="col">Watched</th>
              <th scope="col">Started</th>
            </tr>
          </thead>
          <tbody>
            {items.map((watch) => (
              <tr key={watch.id}>
                <th scope="row">{watch.username || watch.media_user_id}</th>
                <td>{watch.series_name === "" ? "" : watch.item_name}</td>
                <td>{whereLabel(watch)}</td>
                <td>{playMethodLabel(watch.play_method)}</td>
                <td>{formatActiveTime(watch.active_seconds)}</td>
                <td>
                  <time dateTime={watch.started_at}>
                    {watch.started_at.slice(0, 16).replace("T", " ")}
                  </time>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <Pager
        noun="watches"
        hasMore={query.hasNextPage}
        loading={query.isFetchingNextPage}
        failed={query.isFetchNextPageError}
        onMore={query.fetchNextPage}
      />
    </>
  );
}
