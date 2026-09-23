import { expect, it } from "@jest/globals";
import type { Watch } from "../api/playback-schemas.js";
import {
  formatActiveTime,
  formatPosition,
  itemTitle,
  whereLabel,
} from "./watch-format.js";

const base: Watch = {
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
  position_ms: 754_000,
  paused: false,
  play_method: "direct_play",
  active_seconds: 754,
  started_at: "2026-09-24T19:00:00Z",
};

it("titles an episode with its series and numbering", () => {
  expect(itemTitle(base)).toBe("Fringe S01E01 · Pilot");
  expect(itemTitle({ ...base, season_number: null })).toBe("Fringe · Pilot");
  expect(itemTitle({ ...base, series_name: "", item_name: "Heat" })).toBe(
    "Heat",
  );
  expect(itemTitle({ ...base, series_name: "", item_name: "" })).toBe("i-1");
});

it("formats positions and watch time in readable units", () => {
  expect(formatPosition(754_000)).toBe("12:34");
  expect(formatPosition(5_400_000)).toBe("1:30:00");
  expect(formatActiveTime(45)).toBe("45 s");
  expect(formatActiveTime(754)).toBe("12 min 34 s");
  expect(formatActiveTime(3_610)).toBe("1 h 0 min");
});

it("names the device and client, falling back to the device id", () => {
  expect(whereLabel(base)).toBe("Living room TV (Jellyfin Web)");
  expect(whereLabel({ ...base, client: "" })).toBe("Living room TV");
  expect(whereLabel({ ...base, device_name: "", client: "" })).toBe("d-tv");
});
