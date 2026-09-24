import { z } from "zod/v4";
import { watchSchema } from "./playback-schemas.js";

// Wire contract for `/api/v1/stats/*`, mirroring the `Stats*` schemas in
// api/openapi.yaml. Unknown response fields are stripped so the backend can
// extend payloads without breaking the UI.
const MAX_DAYS = 365;
const MAX_ZONE_BYTES = 64;
const MAX_BREAKDOWN = 1024;
const int64 = () => z.number().int().min(0).max(Number.MAX_SAFE_INTEGER);

export const statsDaysSchema = z.number().int().min(1).max(MAX_DAYS);

export const statsZoneSchema = z
  .string()
  .min(1)
  .refine((value) => new TextEncoder().encode(value).length <= MAX_ZONE_BYTES);

export const statsTitleKindSchema = z.enum(["movie", "series", "other"]);

export type StatsTitleKind = z.output<typeof statsTitleKindSchema>;

export type StatsParams = Readonly<{
  days: number;
  mediaServerId?: string;
  // A library belongs to one server, so a library filter always carries
  // the server it lives on.
  libraryId?: string;
  timeZone: string;
}>;

const MAX_LIBRARY_ID_BYTES = 128;
const MAX_LIBRARY_NAME = 500;

export const statsLibraryIdSchema = z
  .string()
  .min(1)
  .refine(
    (value) =>
      new TextEncoder().encode(value).length <= MAX_LIBRARY_ID_BYTES &&
      !/[\p{Cc}]/u.test(value),
  );

export const statsWindowSchema = z.object({
  days: statsDaysSchema,
  start: z.iso.datetime({ offset: true }),
  end: z.iso.datetime({ offset: true }),
  media_server_id: z.union([z.literal(""), z.uuid()]),
  time_zone: statsZoneSchema,
});

export type StatsWindow = z.output<typeof statsWindowSchema>;

export const statsTotalsSchema = z.object({
  plays: int64(),
  watch_seconds: int64(),
  unique_users: int64(),
  unique_titles: int64(),
});

export type StatsTotals = z.output<typeof statsTotalsSchema>;

export const statsTitleSchema = z.object({
  kind: statsTitleKindSchema,
  media_server_id: z.uuid(),
  key: z.string(),
  name: z.string(),
  plays: int64(),
  watch_seconds: int64(),
  last_watched_at: z.iso.datetime({ offset: true }),
});

export type StatsTitle = z.output<typeof statsTitleSchema>;

export const statsUserSchema = z.object({
  media_server_id: z.uuid(),
  media_user_id: z.string().min(1),
  username: z.string(),
  plays: int64(),
  watch_seconds: int64(),
  last_watched_at: z.iso.datetime({ offset: true }),
});

export type StatsUser = z.output<typeof statsUserSchema>;

export const statsBreakdownSchema = z.object({
  name: z.string(),
  plays: int64(),
  watch_seconds: int64(),
});

export type StatsBreakdown = z.output<typeof statsBreakdownSchema>;

export const statsDailyBucketSchema = z.object({
  date: z.iso.date(),
  plays: int64(),
  watch_seconds: int64(),
});

export type StatsDailyBucket = z.output<typeof statsDailyBucketSchema>;

const breakdowns = {
  clients: z.array(statsBreakdownSchema).max(MAX_BREAKDOWN),
  devices: z.array(statsBreakdownSchema).max(MAX_BREAKDOWN),
  play_methods: z.array(statsBreakdownSchema).max(8),
};

export const statsOverviewSchema = z.object({
  window: statsWindowSchema,
  totals: statsTotalsSchema,
  titles: z.array(statsTitleSchema).max(15),
  users: z.array(statsUserSchema).max(5),
  ...breakdowns,
});

export type StatsOverview = z.output<typeof statsOverviewSchema>;

export const statsDailySchema = z.object({
  window: statsWindowSchema,
  items: z.array(statsDailyBucketSchema).max(367),
});

export type StatsDaily = z.output<typeof statsDailySchema>;

export const statsPatternsSchema = z.object({
  window: statsWindowSchema,
  weekdays: z
    .array(
      z.object({ weekday: z.number().int().min(0).max(6), plays: int64() }),
    )
    .length(7),
  hours: z
    .array(z.object({ hour: z.number().int().min(0).max(23), plays: int64() }))
    .length(24),
});

export type StatsPatterns = z.output<typeof statsPatternsSchema>;

export const statsTitlesSchema = z.object({
  window: statsWindowSchema,
  kind: statsTitleKindSchema,
  items: z.array(statsTitleSchema).max(50),
});

export type StatsTitles = z.output<typeof statsTitlesSchema>;

export const statsUsersSchema = z.object({
  window: statsWindowSchema,
  items: z.array(statsUserSchema).max(50),
});

export type StatsUsers = z.output<typeof statsUsersSchema>;

export const statsLibrarySchema = z.object({
  media_server_id: z.uuid(),
  library_id: z.string().max(MAX_LIBRARY_ID_BYTES),
  library_name: z.string().max(MAX_LIBRARY_NAME),
  plays: int64(),
  watch_seconds: int64(),
  unique_users: int64(),
  unique_titles: int64(),
  last_watched_at: z.iso.datetime({ offset: true }),
});

export type StatsLibrary = z.output<typeof statsLibrarySchema>;

export const statsLibrariesSchema = z.object({
  window: statsWindowSchema,
  items: z.array(statsLibrarySchema).max(50),
});

export type StatsLibraries = z.output<typeof statsLibrariesSchema>;

export const statsUserDetailSchema = z.object({
  window: statsWindowSchema,
  totals: statsTotalsSchema,
  titles: z.array(statsTitleSchema).max(15),
  ...breakdowns,
  daily: z.array(statsDailyBucketSchema).max(367),
  watches: z.array(watchSchema).max(20),
});

export type StatsUserDetail = z.output<typeof statsUserDetailSchema>;

export const mediaUserIdSchema = z
  .string()
  .min(1)
  .refine((value) => new TextEncoder().encode(value).length <= 256);
