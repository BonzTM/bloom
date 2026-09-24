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

// What was being delivered at one moment: mirrors `StreamDetails`. Every
// field is optional so a direct-play sample carries only what it knows.
export const streamDetailsSchema = z.object({
  container: z.string().max(64).optional(),
  video_codec: z.string().max(64).optional(),
  audio_codec: z.string().max(64).optional(),
  bitrate: z.number().int().min(0).max(2_147_483_647).optional(),
  width: z.number().int().min(0).max(65_535).optional(),
  height: z.number().int().min(0).max(65_535).optional(),
  framerate: z.number().min(0).max(1000).optional(),
  audio_channels: z.number().int().min(0).max(64).optional(),
  is_video_direct: z.boolean().optional(),
  is_audio_direct: z.boolean().optional(),
  transcode_reasons: z.array(z.string().min(1).max(64)).max(16).optional(),
});

export type StreamDetails = z.output<typeof streamDetailsSchema>;

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
  stream: streamDetailsSchema.optional(),
});

export type Watch = z.output<typeof watchSchema>;

// One sample of a watch's series, newest first from the server.
export const playbackPositionSchema = z.object({
  observed_at: z.iso.datetime({ offset: true }),
  position_ms: int64(),
  paused: z.boolean(),
  play_method: playMethodSchema,
  stream: streamDetailsSchema.optional(),
});

export type PlaybackPosition = z.output<typeof playbackPositionSchema>;

export const playbackPositionsSchema = z.object({
  items: z.array(playbackPositionSchema).max(512),
});

export type PlaybackPositions = z.output<typeof playbackPositionsSchema>;

export const watchIdSchema = z.uuid();

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
