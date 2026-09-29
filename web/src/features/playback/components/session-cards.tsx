import { useId, type ReactNode } from "react";
import { Link } from "react-router-dom";
import type { Watch } from "../api/playback-schemas.js";
import { ItemImage } from "./item-image.js";
import {
  formatActiveTime,
  formatPosition,
  itemTitle,
  playMethodLabel,
  watchPath,
  whereLabel,
} from "./watch-format.js";

// One card per active session: artwork, who, what, how far along, and one
// line of where and how. Each card is an article named by the person, so
// the list reads as "alice, bob" and each card's details follow. The stream
// details live on the watch's own page.
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
  const person = watch.username || watch.media_user_id;
  const episode = watch.item_type === "Episode";
  return (
    <article
      className={episode ? "session-card session-card-wide" : "session-card"}
      aria-labelledby={id}
    >
      <Link
        to={watchPath(watch.id)}
        className="session-card-art"
        aria-hidden="true"
        tabIndex={-1}
      >
        <ItemImage
          mediaServerId={watch.media_server_id}
          itemId={watch.item_id}
          title={watch.series_name === "" ? watch.item_name : watch.series_name}
          shape={episode ? "wide" : "poster"}
        />
      </Link>
      <div className="session-card-body">
        <h3 id={id} className="session-card-user">
          <span className="avatar avatar-small" aria-hidden="true">
            {person.slice(0, 1).toUpperCase()}
          </span>
          {person}
        </h3>
        <p className="session-card-title">
          <Link to={watchPath(watch.id)}>{title}</Link>
        </p>
        <Progress watch={watch} />
        <p className="session-card-meta">
          {whereLabel(watch)} · {watch.media_server_name} ·{" "}
          {playMethodLabel(watch.play_method)} · watched{" "}
          {formatActiveTime(watch.active_seconds)}
        </p>
        <Link to={watchPath(watch.id)} className="session-card-link">
          Details
          <span className="visually-hidden">
            {" "}
            of {title} by {person}
          </span>
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
    <span className="badge badge-neutral">Paused</span>
  ) : (
    <span className="badge badge-success">Playing</span>
  );
  if (watch.runtime_ms === null || watch.runtime_ms <= 0) {
    return (
      <p className="session-card-position">
        <span>
          <time>{position}</time> {paused}
        </span>
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
        <time>{position}</time> of <time>{runtime}</time> {paused}
      </span>
    </p>
  );
}
