import { useId, type ReactNode } from "react";
import { AsyncStatus } from "../../../components/async-status.js";
import {
  LoadFailed,
  Pager,
  RefreshFailed,
} from "../../requests/components/list-states.js";
import type { TimelineEntry } from "../api/playback-schemas.js";
import type { useTimeline } from "../hooks/playback-queries.js";
import { formatActiveTime } from "./watch-format.js";

type TimelineQuery = ReturnType<typeof useTimeline>;

// The gaps a person may fold sittings by, in seconds.
export const TIMELINE_GAPS: readonly (readonly [number, string])[] = [
  [3_600, "1 hour"],
  [21_600, "6 hours"],
  [86_400, "1 day"],
  [604_800, "1 week"],
];

export const DEFAULT_TIMELINE_GAP = 21_600;

type UserTimelineProps = Readonly<{
  query: TimelineQuery;
  gapSeconds: number;
  onGapChange: (gapSeconds: number) => void;
}>;

// One person's watches grouped into sittings: plays of the same item closer
// together than the gap fold into one entry with a play count.
export function UserTimeline({
  query,
  gapSeconds,
  onGapChange,
}: UserTimelineProps): ReactNode {
  const id = useId();
  return (
    <>
      <div className="filter">
        <label htmlFor={id}>Fold plays closer than</label>
        <select
          id={id}
          value={String(gapSeconds)}
          onChange={(event) => {
            onGapChange(Number(event.target.value));
          }}
        >
          {TIMELINE_GAPS.map(([seconds, label]) => (
            <option key={seconds} value={String(seconds)}>
              {label}
            </option>
          ))}
        </select>
      </div>
      <TimelineBody query={query} />
    </>
  );
}

function TimelineBody({
  query,
}: Readonly<{ query: TimelineQuery }>): ReactNode {
  if (query.status === "pending") {
    return <AsyncStatus>Loading the timeline…</AsyncStatus>;
  }
  if (query.status === "error" && query.data === undefined) {
    return <LoadFailed noun="timeline" onRetry={query.refetch} />;
  }
  const items = query.data.pages.flatMap((page) => page.items);
  const refreshFailed = query.isError && !query.isFetchNextPageError;
  return (
    <>
      {refreshFailed ? (
        <RefreshFailed
          noun="timeline"
          retrying={query.isRefetching}
          onRetry={query.refetch}
        />
      ) : null}
      {items.length === 0 ? (
        <p>Nothing has been recorded for this person.</p>
      ) : (
        <ol className="timeline" aria-label="Sittings, newest first">
          {items.map((entry) => (
            <TimelineRow
              key={`${entry.item_id}/${entry.first_started_at}`}
              entry={entry}
            />
          ))}
        </ol>
      )}
      <Pager
        noun="timeline"
        hasMore={query.hasNextPage}
        loading={query.isFetchingNextPage}
        failed={query.isFetchNextPageError}
        onMore={query.fetchNextPage}
      />
    </>
  );
}

function entryTitle(entry: TimelineEntry): string {
  if (entry.series_name !== "" && entry.item_name !== "") {
    return `${entry.series_name} · ${entry.item_name}`;
  }
  return entry.item_name === "" ? entry.item_id : entry.item_name;
}

function TimelineRow({ entry }: Readonly<{ entry: TimelineEntry }>): ReactNode {
  const plays =
    entry.play_count === 1 ? "1 play" : `${String(entry.play_count)} plays`;
  return (
    <li className="timeline-event">
      <time className="timeline-time" dateTime={entry.first_started_at}>
        {entry.first_started_at.slice(0, 16).replace("T", " ")}
      </time>
      <span className="timeline-title">
        {entryTitle(entry)}
        {entry.item_type === "" ? null : (
          <span className="item-type"> {entry.item_type}</span>
        )}
      </span>
      <span className="timeline-detail">
        {plays} · {formatActiveTime(entry.active_seconds)}
        {entry.library_name === "" ? "" : ` · ${entry.library_name}`}
        {entry.last_ended_at === undefined
          ? ""
          : ` · until ${entry.last_ended_at.slice(0, 16).replace("T", " ")}`}
      </span>
    </li>
  );
}
