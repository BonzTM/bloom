import { z } from "zod/v4";

// Wire contract for `/api/v1/playback/now` and `/api/v1/playback/history`,
// mirroring `PlaybackWatch`, `PlaybackHistoryWatch`, `PlaybackNowResponse`,
// and `PlaybackHistoryResponse` in api/openapi.yaml. Unknown response fields
// are stripped so the backend can extend payloads without breaking the UI.
// The contract allows up to 1,024 live watches and pages history by 100.
const MAX_NOW_ITEMS = 1024;
const MAX_PAGE_ITEMS = 100;
const MAX_CURSOR_LENGTH = 256;
// int64 on the wire; the UI keeps to what JavaScript represents exactly.
const int64 = () => z.number().int().min(0).max(Number.MAX_SAFE_INTEGER);

export const playMethodSchema = z.enum([
  "direct_play",
  "direct_stream",
  "transcode",
  "unknown",
]);

export type PlayMethod = z.output<typeof playMethodSchema>;

export const watchSchema = z.object({
  id: z.uuid(),
  media_server_id: z.uuid(),
  media_server_name: z.string().min(1),
  media_user_id: z.string().min(1),
  username: z.string(),
  device_id: z.string().min(1),
  device_name: z.string(),
  client: z.string(),
  item_id: z.string().min(1),
  item_name: z.string(),
  item_type: z.string(),
  series_name: z.string(),
  season_number: z.int32().nullable(),
  episode_number: z.int32().nullable(),
  position_ms: int64(),
  paused: z.boolean(),
  play_method: playMethodSchema,
  active_seconds: int64(),
  started_at: z.iso.datetime({ offset: true }),
  ended_at: z.iso.datetime({ offset: true }).optional(),
});

export type Watch = z.output<typeof watchSchema>;

export const historyWatchSchema = watchSchema.extend({
  ended_at: z.iso.datetime({ offset: true }),
});

export type HistoryWatch = z.output<typeof historyWatchSchema>;

export const nowPlayingSchema = z.object({
  items: z.array(watchSchema).max(MAX_NOW_ITEMS),
});

export type NowPlaying = z.output<typeof nowPlayingSchema>;

// An empty `next_cursor` marks the final page.
export const historyPageSchema = z.object({
  items: z.array(historyWatchSchema).max(MAX_PAGE_ITEMS),
  next_cursor: z.string().max(MAX_CURSOR_LENGTH),
});

export type HistoryPage = z.output<typeof historyPageSchema>;

export const historyCursorSchema = z.string().min(1).max(MAX_CURSOR_LENGTH);

export const mediaServerIdSchema = z.uuid();
