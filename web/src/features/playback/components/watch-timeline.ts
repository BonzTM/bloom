import type { PlaybackPosition } from "../api/playback-schemas.js";
import {
  formatPosition,
  playMethodLabel,
  streamSummary,
  transcodeReasonsLabel,
} from "./watch-format.js";

export type WatchEvent = Readonly<{
  at: string;
  position: string;
  what: string;
  detail: string;
}>;

// A jump in position larger than the time that passed, beyond this much,
// reads as a seek rather than playback drift.
const SEEK_TOLERANCE_MS = 30_000;

// Collapse a sample series into the moments worth reading: the start, every
// pause and resume, every change of play method or stream, every seek,
// and the last sample. Samples arrive newest first; events come out oldest
// first because a timeline reads down the page.
export function watchTimeline(
  samples: readonly PlaybackPosition[],
): readonly WatchEvent[] {
  const ordered = [...samples].reverse();
  const events: WatchEvent[] = [];
  let previous: PlaybackPosition | undefined;
  for (const sample of ordered) {
    if (previous === undefined) {
      events.push(event(sample, "Started", delivery(sample)));
    } else {
      events.push(...changes(previous, sample));
    }
    previous = sample;
  }
  const last = ordered.at(-1);
  if (last !== undefined && ordered.length > 1) {
    events.push(event(last, "Last seen", last.paused ? "Paused" : "Playing"));
  }
  return events;
}

function changes(
  previous: PlaybackPosition,
  sample: PlaybackPosition,
): WatchEvent[] {
  const out: WatchEvent[] = [];
  if (sample.paused !== previous.paused) {
    out.push(event(sample, sample.paused ? "Paused" : "Resumed", ""));
  }
  if (sample.play_method !== previous.play_method) {
    out.push(
      event(
        sample,
        `Switched to ${playMethodLabel(sample.play_method).toLowerCase()}`,
        delivery(sample),
      ),
    );
  } else if (streamSummary(sample.stream) !== streamSummary(previous.stream)) {
    out.push(event(sample, "Stream changed", delivery(sample)));
  }
  if (seeked(previous, sample)) {
    out.push(
      event(sample, "Seeked", `from ${formatPosition(previous.position_ms)}`),
    );
  }
  return out;
}

function seeked(previous: PlaybackPosition, sample: PlaybackPosition): boolean {
  const elapsed =
    Date.parse(sample.observed_at) - Date.parse(previous.observed_at);
  const moved = sample.position_ms - previous.position_ms;
  const expected = previous.paused ? 0 : Math.max(elapsed, 0);
  return Math.abs(moved - expected) > SEEK_TOLERANCE_MS;
}

function delivery(sample: PlaybackPosition): string {
  const parts = [
    playMethodLabel(sample.play_method),
    streamSummary(sample.stream),
  ];
  const reasons = transcodeReasonsLabel(sample.stream);
  if (reasons !== "") {
    parts.push(`because ${reasons}`);
  }
  return parts.filter((part) => part !== "").join(" · ");
}

function event(
  sample: PlaybackPosition,
  what: string,
  detail: string,
): WatchEvent {
  return {
    at: sample.observed_at,
    position: formatPosition(sample.position_ms),
    what,
    detail,
  };
}
