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
  type Watch,
} from "../features/playback/api/playback-schemas.js";
import { ItemImage } from "../features/playback/components/item-image.js";
import {
  formatActiveTime,
  formatPosition,
  itemTitle,
  playMethodLabel,
  streamSummary,
  transcodeReasonsLabel,
  whereLabel,
} from "../features/playback/components/watch-format.js";
import { watchTimeline } from "../features/playback/components/watch-timeline.js";
import {
  useKnownWatch,
  useWatch,
  useWatchPositions,
} from "../features/playback/hooks/playback-queries.js";
import { accessDenial, ApiError } from "../lib/api/errors.js";
import { AccessDeniedRoute } from "./access-denied-route.js";
import { NotFoundRoute } from "./not-found-route.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

// One watch: what was watched, by whom, how it was delivered, and what
// happened along the way. The watch is read on its own; until it arrives
// the copy from the list the person came from stands in.
export default function WatchRoute(): ReactNode {
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
  const known = useKnownWatch(accountId, id);
  const fetched = useWatch(accountId, id);
  const positions = useWatchPositions(accountId, id);
  const watch = fetched.data ?? known;
  usePageTitle(pageTitle(watch === undefined ? "Watch" : itemTitle(watch)));
  const denial = accessDenial(positions.error) ?? accessDenial(fetched.error);
  useSessionRecheck(
    denial !== undefined,
    Math.max(positions.errorUpdatedAt, fetched.errorUpdatedAt),
  );
  if (denial === "forbidden") {
    return <AccessDeniedRoute />;
  }
  if (isNotFound(positions.error) || isNotFound(fetched.error)) {
    return <NotFoundRoute />;
  }
  return (
    <>
      <p>
        <Link to="/admin/playback">Back to playback</Link>
      </p>
      {watch === undefined ? (
        <h1>Watch</h1>
      ) : (
        <WatchHero watch={watch} latest={positions.data?.items[0]} />
      )}
      <section aria-labelledby="watch-timeline-heading" className="card">
        <h2 id="watch-timeline-heading">What happened</h2>
        <p className="section-intro">
          When it started, paused, resumed, seeked, or changed how it was
          delivered.
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
          <Timeline items={positions.data.items} />
        )}
      </section>
    </>
  );
}

// The watch as a card: artwork, title, who and where, and the numbers that
// matter. The latest sample carries the stream in play right now.
function WatchHero({
  watch,
  latest,
}: Readonly<{
  watch: Watch;
  latest: PlaybackPosition | undefined;
}>): ReactNode {
  const stream = streamSummary(latest?.stream ?? watch.stream);
  const reasons = transcodeReasonsLabel(latest?.stream ?? watch.stream);
  const episode = watch.item_type === "Episode";
  const facts: readonly (readonly [string, string])[] = [
    ["Watched", formatActiveTime(watch.active_seconds)],
    ["Position", positionLabel(watch)],
    ["Delivery", playMethodLabel(latest?.play_method ?? watch.play_method)],
    ["Stream", stream === "" ? "Unknown" : stream],
    ["Where", whereLabel(watch)],
    ["Server", watch.media_server_name],
  ];
  return (
    <header className={episode ? "watch-hero watch-hero-wide" : "watch-hero"}>
      <div className="watch-hero-art">
        <ItemImage
          mediaServerId={watch.media_server_id}
          itemId={watch.item_id}
          title={watch.series_name === "" ? watch.item_name : watch.series_name}
          shape={episode ? "wide" : "poster"}
        />
      </div>
      <div className="watch-hero-body">
        <p className="watch-hero-person">
          <span className="avatar avatar-small" aria-hidden="true">
            {(watch.username || watch.media_user_id).slice(0, 1).toUpperCase()}
          </span>
          {watch.username || watch.media_user_id}
        </p>
        <h1>{itemTitle(watch)}</h1>
        <p className="watch-hero-when">
          {watch.ended_at === undefined ? (
            <span className="badge badge-success">Playing</span>
          ) : (
            <span className="badge badge-neutral">Finished</span>
          )}{" "}
          started{" "}
          <time dateTime={watch.started_at}>
            {watch.started_at.slice(0, 16).replace("T", " ")}
          </time>
          {watch.library_name === "" ? "" : ` · ${watch.library_name}`}
        </p>
        <dl className="facts watch-facts">
          {facts.map(([label, value]) => (
            <div key={label}>
              <dt>{label}</dt>
              <dd>{value}</dd>
            </div>
          ))}
        </dl>
        {reasons === "" ? null : (
          <p className="row-detail">Transcoding because: {reasons}</p>
        )}
      </div>
    </header>
  );
}

function isNotFound(error: unknown): boolean {
  return error instanceof ApiError && error.status === 404;
}

function positionLabel(watch: Watch): string {
  const position = formatPosition(watch.position_ms);
  if (watch.runtime_ms === null || watch.runtime_ms <= 0) {
    return position;
  }
  return `${position} of ${formatPosition(watch.runtime_ms)}`;
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
