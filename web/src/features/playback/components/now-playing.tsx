import type { ReactNode } from "react";
import { AsyncStatus } from "../../../components/async-status.js";
import type { useNowPlaying } from "../hooks/playback-queries.js";
import { SessionCards } from "./session-cards.js";

type NowPlayingQuery = ReturnType<typeof useNowPlaying>;

// The route owns the query and decides what a 401 or 403 means; this renders
// loading, empty, rows, a failed refresh that keeps the rows, and a failed
// first load.
export function NowPlaying({
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
        <SessionCards watches={items} label="Playing now" />
      )}
    </>
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
