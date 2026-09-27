import { describe, expect, it } from "@jest/globals";
import type { PlaybackPosition } from "../api/playback-schemas.js";
import { watchTimeline } from "./watch-timeline.js";

function sample(
  at: string,
  positionMs: number,
  patch: Partial<PlaybackPosition> = {},
): PlaybackPosition {
  return {
    observed_at: at,
    position_ms: positionMs,
    paused: false,
    play_method: "direct_play",
    ...patch,
  };
}

describe("watchTimeline", () => {
  it("keeps the start, changes, seeks, and the last sample", () => {
    // Newest first, as the API returns them: five-second polls with one
    // pause, one resume, a seek forward, and a switch to a transcode.
    const samples = [
      sample("2026-09-24T19:01:00Z", 61_000, { play_method: "transcode" }),
      sample("2026-09-24T19:00:55Z", 56_000),
      sample("2026-09-24T19:00:50Z", 51_000),
      sample("2026-09-24T19:00:45Z", 600_000),
      sample("2026-09-24T19:00:40Z", 10_000),
      sample("2026-09-24T19:00:35Z", 5_000, { paused: true }),
      sample("2026-09-24T19:00:30Z", 5_000, { paused: true }),
      sample("2026-09-24T19:00:25Z", 5_000),
      sample("2026-09-24T19:00:20Z", 0),
    ];
    const events = watchTimeline(samples);
    expect(events.map((e) => `${e.what} ${e.position}`)).toEqual([
      "Started 0:00",
      "Paused 0:05",
      "Resumed 0:10",
      "Seeked 10:00",
      "Seeked 0:51",
      "Switched to transcode 1:01",
      "Last seen 1:01",
    ]);
  });

  it("reads a single sample as a start only", () => {
    expect(watchTimeline([sample("2026-09-24T19:00:20Z", 0)])).toHaveLength(1);
  });
});
