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

export const playbackSourceSchema = z.enum([
  "poll",
  "websocket",
  "webhook",
  "import",
]);

export type PlaybackSource = z.output<typeof playbackSourceSchema>;

export const importSourceSchema = z.enum([
  "playback_reporting",
  "jellystat",
  "bloom_export",
  "jellyfin_userdata",
]);

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
  device_id: z.string(),
  device_name: z.string(),
  client: z.string(),
  item_id: z.string().min(1),
  item_name: z.string(),
  item_type: z.string(),
  series_id: z.string(),
  series_name: z.string(),
  library_id: z.string(),
  library_name: z.string(),
  season_number: z.int32().nullable(),
  episode_number: z.int32().nullable(),
  position_ms: int64(),
  // The item\'s length when the server reported one; null otherwise.
  runtime_ms: int64().nullable(),
  paused: z.boolean(),
  play_method: playMethodSchema,
  active_seconds: int64(),
  started_at: z.iso.datetime({ offset: true }),
  ended_at: z.iso.datetime({ offset: true }).optional(),
  stream: streamDetailsSchema.optional(),
  source: playbackSourceSchema,
  import_source: importSourceSchema.optional(),
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

// ---- activity (stats.read.all): every watch across servers and people,
// filtered and paged by keyset.

const MAX_FILTER_BYTES = 128;
const filterText = z
  .string()
  .min(1)
  .refine((value) => !/\p{Cc}/u.test(value))
  .refine(
    (value) => new TextEncoder().encode(value).length <= MAX_FILTER_BYTES,
  );

export const activityFilterSchema = z.object({
  mediaServerId: z.uuid().optional(),
  q: filterText.optional(),
  playMethod: playMethodSchema.optional(),
  source: playbackSourceSchema.optional(),
  startedAfter: z.iso.datetime({ offset: true }).optional(),
  startedBefore: z.iso.datetime({ offset: true }).optional(),
});

export type ActivityFilter = z.output<typeof activityFilterSchema>;

export const activityCursorSchema = historyCursorSchema;

const pageLimitSchema = z.number().int().min(1).max(MAX_PAGE_ITEMS);

// Activity pages carry open watches too, so `ended_at` stays optional.
export const activityPageSchema = z.object({
  items: z.array(watchSchema).max(MAX_PAGE_ITEMS),
  limit: pageLimitSchema,
  next_cursor: z.string().max(MAX_CURSOR_LENGTH),
});

export type ActivityPage = z.output<typeof activityPageSchema>;

// ---- timeline (stats.read.all): one person's watches grouped into sittings.

export const timelineGapSchema = z.number().int().min(1).max(604_800);

// The timeline route bounds the media user id tighter than statistics do.
export const timelineUserIdSchema = filterText;

export const timelineEntrySchema = z.object({
  media_server_id: z.uuid(),
  media_user_id: z.string().min(1),
  username: z.string(),
  item_id: z.string().min(1),
  item_name: z.string(),
  item_type: z.string(),
  series_id: z.string(),
  series_name: z.string(),
  library_id: z.string(),
  library_name: z.string(),
  first_started_at: z.iso.datetime({ offset: true }),
  last_ended_at: z.iso.datetime({ offset: true }).optional(),
  play_count: z.number().int().min(1).max(500),
  active_seconds: int64(),
});

export type TimelineEntry = z.output<typeof timelineEntrySchema>;

export const timelinePageSchema = z.object({
  items: z.array(timelineEntrySchema).max(MAX_PAGE_ITEMS),
  limit: pageLimitSchema,
  gap_seconds: timelineGapSchema,
  next_cursor: z.string().max(MAX_CURSOR_LENGTH),
});

export type TimelinePage = z.output<typeof timelinePageSchema>;
