import { z } from "zod";
import { watchSchema } from "../../playback/api/playback-schemas.js";

// Wire shapes mirror api/openapi.yaml (Library, Catalog*).
const int64 = () => z.number().int().min(0).max(Number.MAX_SAFE_INTEGER);
const stamp = () => z.iso.datetime({ offset: true });

export const librarySchema = z.object({
  id: z.string().min(1),
  name: z.string().min(1),
  type: z.string(),
});

export type Library = z.output<typeof librarySchema>;

export const librariesResponseSchema = z.object({
  items: z.array(librarySchema).max(256),
});

export const catalogTypeSummarySchema = z.object({
  item_type: z.string().min(1),
  items: int64(),
  plays: int64(),
  watch_seconds: int64(),
});

export type CatalogTypeSummary = z.output<typeof catalogTypeSummarySchema>;

export const catalogLibrarySummarySchema = z.object({
  library_id: z.string().min(1),
  types: z.array(catalogTypeSummarySchema).max(10_000),
});

export type CatalogLibrarySummary = z.output<
  typeof catalogLibrarySummarySchema
>;

export const catalogLibrariesResponseSchema = z.object({
  items: z.array(catalogLibrarySummarySchema).max(256),
});

export const catalogItemSchema = z.object({
  media_server_id: z.uuid(),
  item_id: z.string().min(1),
  library_id: z.string().min(1),
  parent_id: z.string(),
  item_type: z.string().min(1),
  name: z.string().min(1),
  series_id: z.string(),
  series_name: z.string(),
  season_id: z.string(),
  season_number: z.int32().optional(),
  index_number: z.int32().optional(),
  runtime_ms: int64().optional(),
  premiere_date: stamp().optional(),
  date_created: stamp().optional(),
  production_year: z.number().int().min(0).max(9999).optional(),
  community_rating: z.number().min(0).max(100).optional(),
  genres: z.array(z.string().min(1)).max(64),
  primary_image_tag: z.string(),
  archived: z.boolean(),
  first_seen_at: stamp(),
  last_seen_at: stamp(),
  updated_at: stamp(),
  plays: int64(),
  watch_seconds: int64(),
  unique_users: int64(),
  first_played_at: stamp().optional(),
  last_played_at: stamp().optional(),
});

export type CatalogItem = z.output<typeof catalogItemSchema>;

export const catalogItemsResponseSchema = z.object({
  items: z.array(catalogItemSchema).max(100),
  limit: z.number().int().min(1).max(100),
  offset: z.number().int().min(0).max(1_000_000),
});

export type CatalogItemsPage = z.output<typeof catalogItemsResponseSchema>;

export const catalogChildSummarySchema = z.object({
  item_type: z.string().min(1),
  items: int64(),
});

export const catalogItemDetailSchema = z.object({
  item: catalogItemSchema,
  children: z.array(catalogChildSummarySchema).max(100),
});

export type CatalogItemDetail = z.output<typeof catalogItemDetailSchema>;

export const catalogHistoryResponseSchema = z.object({
  items: z.array(watchSchema).max(100),
  limit: z.number().int().min(1).max(100),
  offset: z.number().int().min(0).max(1_000_000),
});

export type CatalogHistoryPage = z.output<typeof catalogHistoryResponseSchema>;

export const catalogGenreSummarySchema = z.object({
  genre: z.string().min(1),
  items: int64(),
  plays: int64(),
  watch_seconds: int64(),
});

export type CatalogGenreSummary = z.output<typeof catalogGenreSummarySchema>;

export const catalogGenresResponseSchema = z.object({
  items: z.array(catalogGenreSummarySchema).max(1000),
});

export const catalogSyncResponseSchema = z.object({
  media_server_id: z.uuid(),
  state: z.literal("pending"),
});

export const catalogSortSchema = z.enum([
  "name",
  "date_added",
  "premiere_date",
  "plays",
  "watch_time",
  "last_played",
]);

export type CatalogSort = z.output<typeof catalogSortSchema>;

export const catalogOrderSchema = z.enum(["asc", "desc"]);

export type CatalogOrder = z.output<typeof catalogOrderSchema>;

export const CATALOG_PAGE = 50;
export const MAX_CATALOG_DAYS = 3650;

export const catalogDaysSchema = z.number().int().min(1).max(MAX_CATALOG_DAYS);
export const mediaServerIdSchema = z.uuid();
export const libraryIdSchema = z.string().min(1).max(128);
export const itemIdSchema = z.string().min(1).max(128);

export type CatalogItemsParams = Readonly<{
  days?: number;
  itemType?: string;
  archived?: boolean;
  sort: CatalogSort;
  order: CatalogOrder;
}>;
