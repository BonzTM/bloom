import { expect, it } from "@jest/globals";
import { historyPageSchema, nowPlayingSchema } from "./playback-schemas.js";

const watch = {
  id: "7b2c3d4e-0000-4000-8000-000000000001",
  media_server_id: "3d7f1a2b-0000-4000-8000-000000000001",
  media_server_name: "Cabin",
  media_user_id: "u-alice",
  username: "alice",
  device_id: "d-tv",
  device_name: "Living room TV",
  client: "Jellyfin Web",
  item_id: "i-1",
  item_name: "Pilot",
  item_type: "Episode",
  series_name: "Fringe",
  season_number: 1,
  episode_number: 1,
  position_ms: 754000,
  paused: false,
  play_method: "direct_play",
  active_seconds: 754,
  started_at: "2026-09-24T19:00:00Z",
};

it("accepts live watches and strips fields it does not know", () => {
  const now = nowPlayingSchema.parse({ items: [{ ...watch, extra: 1 }] });
  expect(now.items).toEqual([watch]);
});

it("requires an end for a finished watch and rejects bad values", () => {
  expect(
    historyPageSchema.safeParse({ items: [watch], next_cursor: "" }).success,
  ).toBe(false);
  const page = historyPageSchema.parse({
    items: [{ ...watch, ended_at: "2026-09-24T19:30:00Z" }],
    next_cursor: "",
  });
  expect(page.items[0]?.ended_at).toBe("2026-09-24T19:30:00Z");
  for (const patch of [
    { play_method: "magic" },
    { position_ms: -1 },
    { season_number: 1.5 },
    { id: "1" },
  ]) {
    expect(
      nowPlayingSchema.safeParse({ items: [{ ...watch, ...patch }] }).success,
    ).toBe(false);
  }
});

it("accepts up to 1,024 live watches and pages history by 100", () => {
  const many = (count: number) =>
    Array.from({ length: count }, (_, index) => ({
      ...watch,
      id: `7b2c3d4e-0000-4000-8000-${String(index).padStart(12, "0")}`,
    }));
  expect(nowPlayingSchema.safeParse({ items: many(1024) }).success).toBe(true);
  expect(nowPlayingSchema.safeParse({ items: many(1025) }).success).toBe(false);
  const finished = many(101).map((w) => ({
    ...w,
    ended_at: "2026-09-24T19:30:00Z",
  }));
  expect(
    historyPageSchema.safeParse({ items: finished, next_cursor: "" }).success,
  ).toBe(false);
});

it("bounds numbering to int32 and counters to safe integers", () => {
  expect(
    nowPlayingSchema.safeParse({
      items: [{ ...watch, season_number: 2 ** 31 }],
    }).success,
  ).toBe(false);
  expect(
    nowPlayingSchema.safeParse({
      items: [{ ...watch, active_seconds: Number.MAX_SAFE_INTEGER }],
    }).success,
  ).toBe(true);
  expect(
    nowPlayingSchema.safeParse({
      items: [{ ...watch, position_ms: Number.MAX_SAFE_INTEGER + 2 }],
    }).success,
  ).toBe(false);
});
