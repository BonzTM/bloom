import type { ReactNode } from "react";
import { Link, useParams } from "react-router-dom";
import { AsyncStatus } from "../components/async-status.js";
import {
  useSession,
  useSessionRecheck,
} from "../features/auth/hooks/auth-queries.js";
import {
  watchIdSchema,
  type PlaybackPosition,
} from "../features/playback/api/playback-schemas.js";
import {
  formatPosition,
  playMethodLabel,
  streamSummary,
  transcodeReasonsLabel,
} from "../features/playback/components/watch-format.js";
import { watchTimeline } from "../features/playback/components/watch-timeline.js";
import { useWatchPositions } from "../features/playback/hooks/playback-queries.js";
import { accessDenial, ApiError } from "../lib/api/errors.js";
import { AccessDeniedRoute } from "./access-denied-route.js";
import { NotFoundRoute } from "./not-found-route.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

// One watch's sample series: how far it had got, whether it was paused, and
// what was being delivered at each moment, so a mid-play change from direct
// play to a transcode is visible as two rows.
export default function WatchRoute(): ReactNode {
  usePageTitle(pageTitle("Watch"));
  const { id = "" } = useParams();
  const session = useSession();
  const accountId = session.data?.account.id;
  if (!watchIdSchema.safeParse(id).success) {
    return <NotFoundRoute />;
  }
  if (accountId === undefined) {
    return null;
  }
  return <WatchPage key={`${accountId}/${id}`} accountId={accountId} id={id} />;
}

function WatchPage({
  accountId,
  id,
}: Readonly<{ accountId: string; id: string }>): ReactNode {
  const positions = useWatchPositions(accountId, id);
  const denial = accessDenial(positions.error);
  useSessionRecheck(denial !== undefined, positions.errorUpdatedAt);
  if (denial === "forbidden") {
    return <AccessDeniedRoute />;
  }
  if (positions.error instanceof ApiError && positions.error.status === 404) {
    return <NotFoundRoute />;
  }
  return (
    <>
      <p>
        <Link to="/admin/playback">Back to playback</Link>
      </p>
      <h1>Watch</h1>
      <p className="page-intro">
        What happened during this watch: when it started, paused, resumed,
        seeked, or changed how it was delivered. The raw samples Bloom polled
        are below for anyone who needs them.
      </p>
      {positions.status === "pending" ? (
        <AsyncStatus>Loading the watch…</AsyncStatus>
      ) : positions.status === "error" ? (
        denial === "unauthenticated" ? (
          <AsyncStatus kind="alert">
            The watch could not be loaded because your sign-in could not be
            confirmed.
          </AsyncStatus>
        ) : (
          <>
            <AsyncStatus kind="alert">
              The watch could not be loaded.
            </AsyncStatus>
            <button
              type="button"
              onClick={() => {
                void positions.refetch();
              }}
            >
              Retry
            </button>
          </>
        )
      ) : (
        <>
          <Timeline items={positions.data.items} />
          <details className="samples-details">
            <summary>
              All samples ({String(positions.data.items.length)})
            </summary>
            <SeriesTable items={positions.data.items} />
          </details>
        </>
      )}
    </>
  );
}

// The readable version of the sample series: one row per moment that
// changed something, oldest first.
function Timeline({
  items,
}: Readonly<{ items: readonly PlaybackPosition[] }>): ReactNode {
  const events = watchTimeline(items);
  if (events.length === 0) {
    return <p>No samples have been recorded for this watch.</p>;
  }
  return (
    <ol className="timeline" aria-label="Watch timeline">
      {events.map((event, index) => (
        <li key={`${event.at}-${String(index)}`} className="timeline-event">
          <time dateTime={event.at} className="timeline-time">
            {event.at.slice(11, 19)}
          </time>
          <span className="timeline-what">
            <strong>{event.what}</strong> at {event.position}
          </span>
          {event.detail === "" ? null : (
            <span className="timeline-detail">{event.detail}</span>
          )}
        </li>
      ))}
    </ol>
  );
}

function SeriesTable({
  items,
}: Readonly<{ items: readonly PlaybackPosition[] }>): ReactNode {
  if (items.length === 0) {
    return <p>No samples have been recorded for this watch.</p>;
  }
  return (
    <div
      className="table-scroll"
      role="region"
      aria-label="Watch samples table"
      tabIndex={0}
    >
      <table className="playback-table">
        <caption>Samples, newest first</caption>
        <thead>
          <tr>
            <th scope="col">Observed</th>
            <th scope="col">Position</th>
            <th scope="col">State</th>
            <th scope="col">Method</th>
            <th scope="col">Stream</th>
            <th scope="col">Transcode reasons</th>
          </tr>
        </thead>
        <tbody>
          {items.map((sample) => (
            <tr key={sample.observed_at}>
              <th scope="row">
                <time dateTime={sample.observed_at}>
                  {sample.observed_at.slice(0, 19).replace("T", " ")}
                </time>
              </th>
              <td>{formatPosition(sample.position_ms)}</td>
              <td>{sample.paused ? "Paused" : "Playing"}</td>
              <td>{playMethodLabel(sample.play_method)}</td>
              <td>{streamSummary(sample.stream)}</td>
              <td>{transcodeReasonsLabel(sample.stream)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
