import { useId, type ReactNode } from "react";
import { Link } from "react-router-dom";
import type { Watch } from "../api/playback-schemas.js";
import { ItemImage } from "./item-image.js";
import {
  formatActiveTime,
  formatPosition,
  itemTitle,
  playMethodLabel,
  streamSummary,
  watchPath,
  whereLabel,
} from "./watch-format.js";

// One card per active session: artwork, who, what, where, how far along.
// Each card is an article named by the person, so the list reads as
// "alice, bob" and each card's details follow.
export function SessionCards({
  watches,
  label,
}: Readonly<{ watches: readonly Watch[]; label: string }>): ReactNode {
  return (
    <ul className="session-cards" aria-label={label}>
      {watches.map((watch) => (
        <li key={watch.id}>
          <SessionCard watch={watch} />
        </li>
      ))}
    </ul>
  );
}

function SessionCard({ watch }: Readonly<{ watch: Watch }>): ReactNode {
  const id = useId();
  const title = itemTitle(watch);
  const stream = streamSummary(watch.stream);
  return (
    <article className="session-card" aria-labelledby={id}>
      <ItemImage
        mediaServerId={watch.media_server_id}
        itemId={watch.item_id}
        title={watch.series_name === "" ? watch.item_name : watch.series_name}
        shape={watch.item_type === "Episode" ? "wide" : "poster"}
      />
      <div className="session-card-body">
        <h3 id={id} className="session-card-user">
          {watch.username || watch.media_user_id}
        </h3>
        <p className="session-card-title">
          {title}
          {watch.item_type === "" ? null : (
            <span className="item-type"> {watch.item_type}</span>
          )}
        </p>
        <p className="session-card-meta">
          {whereLabel(watch)} · {watch.media_server_name}
        </p>
        <Progress watch={watch} />
        <p className="session-card-meta">
          {playMethodLabel(watch.play_method)} · watched{" "}
          {formatActiveTime(watch.active_seconds)}
          {stream === "" ? null : ` · ${stream}`}
        </p>
        <Link to={watchPath(watch.id)} className="session-card-link">
          Details
          <span className="visually-hidden"> of {title}</span>
        </Link>
      </div>
    </article>
  );
}

// The position against the item's length when the server reported one;
// the bare position otherwise.
function Progress({ watch }: Readonly<{ watch: Watch }>): ReactNode {
  const position = formatPosition(watch.position_ms);
  const paused = watch.paused ? (
    <span className="badge badge-neutral"> paused</span>
  ) : null;
  if (watch.runtime_ms === null || watch.runtime_ms <= 0) {
    return (
      <p className="session-card-position">
        <time>{position}</time>
        {paused}
      </p>
    );
  }
  const runtime = formatPosition(watch.runtime_ms);
  const clamped = Math.min(watch.position_ms, watch.runtime_ms);
  return (
    <p className="session-card-position">
      <progress
        max={watch.runtime_ms}
        value={clamped}
        aria-label={`Progress through ${itemTitle(watch)}`}
        aria-valuetext={`${position} of ${runtime}`}
      />
      <span>
        <time>{position}</time> of <time>{runtime}</time>
        {paused}
      </span>
    </p>
  );
}
