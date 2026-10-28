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
  // Whether the caller asked to be told when this title becomes available;
  // optional so a server without subscriptions still parses.
  subscribed: z.boolean().optional(),
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

// Rows narrowed to one TMDB genre, production company, or network, with
// their API paths.
export const filteredDiscoverListSchema = z.enum([
  "movies/genres",
  "series/genres",
  "movies/studios",
  "series/networks",
]);

export type FilteredDiscoverList = z.output<typeof filteredDiscoverListSchema>;

// The largest genre, company, or network identifier the server accepts.
export const MAX_DISCOVER_FILTER_ID = 1_000_000_000;

export const discoverFilterIdSchema = z
  .number()
  .int()
  .min(1)
  .max(MAX_DISCOVER_FILTER_ID);

// Where one row's titles come from: a curated list, or a filtered one.
export type DiscoverSource =
  DiscoverList | Readonly<{ list: FilteredDiscoverList; id: number }>;

// The path under /metadata/discover/ for a source, validated so nothing
// unexpected reaches the URL.
export function discoverSourcePath(source: DiscoverSource): string {
  if (typeof source === "string") {
    return discoverListSchema.parse(source);
  }
  const list = filteredDiscoverListSchema.parse(source.list);
  return `${list}/${String(discoverFilterIdSchema.parse(source.id))}`;
}

// Wire contract for `/api/v1/metadata/genres`, mirroring
// `MetadataGenresResponse` in api/openapi.yaml.
const MAX_GENRES = 100;
const MAX_GENRE_NAME_BYTES = 100;

export const metadataGenreSchema = z.object({
  id: discoverFilterIdSchema,
  name: z.string().min(1).pipe(boundedBytes(MAX_GENRE_NAME_BYTES)),
});

export type MetadataGenre = z.output<typeof metadataGenreSchema>;

export const metadataGenresResponseSchema = z.object({
  items: z.array(metadataGenreSchema).max(MAX_GENRES),
});

// The rich detail on a title page, mirroring `MetadataDetails` and its
// parts in api/openapi.yaml.
const MAX_SHORT_TEXT_BYTES = 500;
const shortText = boundedBytes(MAX_SHORT_TEXT_BYTES);
const namedText = z.string().min(1).pipe(shortText);
const isoDateTime = z.iso.datetime({ offset: true });

export const metadataCreditSchema = z.object({
  person_id: z.number().int().min(1),
  name: namedText,
  role: shortText,
  profile_path: shortText,
});

export type MetadataCredit = z.output<typeof metadataCreditSchema>;

export const metadataCompanySchema = z.object({
  id: z.number().int().min(1),
  name: namedText,
  logo_path: shortText,
});

export type MetadataCompany = z.output<typeof metadataCompanySchema>;

export const metadataReleaseTypeSchema = z.enum([
  "premiere",
  "theatrical_limited",
  "theatrical",
  "digital",
  "physical",
  "tv",
]);

export type MetadataReleaseType = z.output<typeof metadataReleaseTypeSchema>;

export const metadataSeriesFactsSchema = z.object({
  creators: z.array(metadataCreditSchema).max(10),
  networks: z.array(metadataCompanySchema).max(10),
  first_air_date: isoDateTime.optional(),
  last_air_date: isoDateTime.optional(),
  next_episode: z
    .object({
      season: z.number().int().min(0).max(MAX_SEASON_NUMBER),
      episode: z.number().int().min(0).max(99_999),
      name: shortText,
      air_date: isoDateTime.optional(),
    })
    .optional(),
  season_count: z.number().int().min(0).max(MAX_SEASON_NUMBER),
  episode_count: z.number().int().min(0).max(99_999),
  in_production: z.boolean(),
});

export type MetadataSeriesFacts = z.output<typeof metadataSeriesFactsSchema>;

export const metadataDetailsSchema = z.object({
  tagline: shortText,
  status: boundedBytes(100),
  certification: boundedBytes(20),
  runtime_minutes: z.number().int().min(0).max(10_000),
  vote_average: z.number().min(0).max(10),
  vote_count: z.number().int().min(0),
  original_language: z.string().regex(/^([a-z]{2,3})?$/),
  imdb_id: z.string().regex(/^(tt[0-9]{1,12})?$/),
  trailer_youtube_key: z.string().regex(/^[A-Za-z0-9_-]{0,32}$/),
  budget: z.number().int().min(0),
  revenue: z.number().int().min(0),
  genres: z.array(z.string().min(1).pipe(boundedBytes(100))).max(10),
  keywords: z.array(namedText).max(40),
  production_countries: z
    .array(z.object({ code: z.string().regex(/^[A-Z]{2}$/), name: namedText }))
    .max(10),
  studios: z.array(metadataCompanySchema).max(10),
  release_dates: z
    .array(z.object({ type: metadataReleaseTypeSchema, date: isoDateTime }))
    .max(6),
  watch_providers: z.array(metadataCompanySchema).max(12),
  cast: z.array(metadataCreditSchema).max(30),
  crew: z.array(metadataCreditSchema).max(8),
  recommendations: z.array(metadataDiscoverTitleSchema).max(MAX_DISCOVER_PAGE),
  similar: z.array(metadataDiscoverTitleSchema).max(MAX_DISCOVER_PAGE),
  series: metadataSeriesFactsSchema.optional(),
});

export type MetadataDetails = z.output<typeof metadataDetailsSchema>;

// `/api/v1/metadata/{movies,series}/{id}/ratings`, mirroring `TitleRatings`
// in api/openapi.yaml. A missing score is one no source knows.
const MAX_VOTES = 100_000_000;
const ratingSourceSchema = z.enum(["radarr", "jellyfin", "omdb"]);
const titleRatingSchema = (maxScore: number) =>
  z.object({
    score: z.number().min(0).max(maxScore),
    votes: z.number().int().min(0).max(MAX_VOTES),
    source: ratingSourceSchema,
  });

export const titleRatingsSchema = z.object({
  imdb: titleRatingSchema(10).optional(),
  rotten_tomatoes: titleRatingSchema(100).optional(),
  metacritic: titleRatingSchema(100).optional(),
});

export type TitleRatings = z.output<typeof titleRatingsSchema>;
export type TitleRating = NonNullable<TitleRatings["imdb"]>;

// A title page's answer always says whether the caller follows the title,
// and carries the rich detail the page shows.
export const metadataTitleDetailSchema = metadataTitleSchema.extend({
  subscribed: z.boolean(),
  details: metadataDetailsSchema,
});

export type MetadataTitleDetail = z.output<typeof metadataTitleDetailSchema>;

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

export const metadataSeriesSchema = metadataTitleDetailSchema.extend({
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
