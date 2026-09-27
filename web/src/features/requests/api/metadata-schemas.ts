import { z } from "zod/v4";
import { mediaKindSchema, utf8Length } from "./requests-schemas.js";

// Wire contract for `/api/v1/metadata/search`, `/api/v1/metadata/movies/{id}`,
// `/api/v1/metadata/series/{id}`, and `POST /api/v1/requests`, mirroring
// `MetadataTitle`, `MetadataSeries`, `MetadataSeason`, and
// `CreateMediaRequest` in api/openapi.yaml.
const MAX_QUERY_LENGTH = 200;
const MAX_RESULTS = 100;
const MAX_SEASONS = 100;
const MAX_SEASON_NUMBER = 999;
const MAX_TITLE_BYTES = 500;
const MAX_OVERVIEW_BYTES = 10_000;
const MAX_POSTER_BYTES = 500;

// A TMDB identifier: decimal digits without a leading zero.
export const PROVIDER_ID_PATTERN = /^[1-9][0-9]{0,19}$/;

export const providerIdSchema = z.string().regex(PROVIDER_ID_PATTERN);

export const searchQuerySchema = z.string().trim().min(1).max(MAX_QUERY_LENGTH);

const boundedBytes = (max: number) =>
  z.string().refine((value) => utf8Length(value) <= max);

export const metadataTitleSchema = z.object({
  kind: mediaKindSchema,
  provider: z.literal("tmdb"),
  provider_id: providerIdSchema,
  title: z.string().min(1).pipe(boundedBytes(MAX_TITLE_BYTES)),
  year: z.number().int().min(0).max(9999),
  overview: boundedBytes(MAX_OVERVIEW_BYTES),
  poster_path: boundedBytes(MAX_POSTER_BYTES),
  backdrop_path: boundedBytes(MAX_POSTER_BYTES),
});

export type MetadataTitle = z.output<typeof metadataTitleSchema>;

// The caller's own request for a title, or none.
export const metadataRequestStateSchema = z.enum([
  "none",
  "pending",
  "approved",
  "processing",
  "available",
  "declined",
  "failed",
]);

export type MetadataRequestState = z.output<typeof metadataRequestStateSchema>;

export const metadataDiscoverTitleSchema = metadataTitleSchema.extend({
  request_state: metadataRequestStateSchema,
});

export type MetadataDiscoverTitle = z.output<
  typeof metadataDiscoverTitleSchema
>;

export const MAX_DISCOVER_PAGE = 20;
export const MAX_DISCOVER_CURSOR_LENGTH = 4;

export const discoverCursorSchema = z
  .string()
  .min(1)
  .max(MAX_DISCOVER_CURSOR_LENGTH);

export const metadataDiscoverResponseSchema = z.object({
  items: z.array(metadataDiscoverTitleSchema).max(MAX_DISCOVER_PAGE),
  next_cursor: z.string().max(MAX_DISCOVER_CURSOR_LENGTH),
});

export type MetadataDiscoverResponse = z.output<
  typeof metadataDiscoverResponseSchema
>;

// The rows the discover page shows, in order, with their API paths.
export const discoverListSchema = z.enum([
  "trending",
  "movies/popular",
  "series/popular",
  "movies/upcoming",
  "series/upcoming",
]);

export type DiscoverList = z.output<typeof discoverListSchema>;

export const metadataSearchResponseSchema = z.object({
  items: z.array(metadataTitleSchema).max(MAX_RESULTS),
});

export const metadataSeasonSchema = z.object({
  number: z.number().int().min(0).max(MAX_SEASON_NUMBER),
  name: z.string().min(1).pipe(boundedBytes(MAX_TITLE_BYTES)),
  episode_count: z.number().int().min(0).max(9999),
  air_date: z.iso.datetime({ offset: true }).optional(),
});

export type MetadataSeason = z.output<typeof metadataSeasonSchema>;

export const metadataSeriesSchema = metadataTitleSchema.extend({
  seasons: z.array(metadataSeasonSchema).max(MAX_SEASONS),
});

export type MetadataSeries = z.output<typeof metadataSeriesSchema>;

// The exact request the server accepts. Seasons are empty for a movie and
// distinct, ascending numbers from 1 for a series.
export const createMediaRequestSchema = z
  .strictObject({
    kind: mediaKindSchema,
    provider_id: providerIdSchema,
    profile_id: z.uuid(),
    seasons: z
      .array(z.number().int().min(1).max(MAX_SEASON_NUMBER))
      .max(MAX_SEASONS)
      .refine((seasons) => new Set(seasons).size === seasons.length),
  })
  .refine((input) =>
    input.kind === "movie"
      ? input.seasons.length === 0
      : input.seasons.length > 0,
  );

export type CreateMediaRequest = z.output<typeof createMediaRequestSchema>;
