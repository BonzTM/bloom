import { z } from "zod/v4";
import { mediaKindSchema, utf8Length } from "./requests-schemas.js";

// Wire contract for `/api/v1/download-managers` and
// `/api/v1/requests/{id}/progress`, mirroring `DownloadManager`,
// `CreateDownloadManagerRequest`, `CreateDownloadManagerResponse`,
// `DownloadManagerOptions`, and `RequestProgress` in api/openapi.yaml.
const MAX_PAGE_ITEMS = 100;
const MAX_CURSOR_LENGTH = 400;
const MAX_NAME_BYTES = 100;
const MAX_URL_LENGTH = 2048;
const MAX_API_KEY_LENGTH = 4096;
const MAX_OPTION_BYTES = 500;
const MAX_OPTIONS = 256;

export const downloadManagerKindSchema = z.enum(["radarr", "sonarr"]);

export type DownloadManagerKind = z.output<typeof downloadManagerKindSchema>;

const boundedBytes = (max: number) =>
  z
    .string()
    .min(1)
    .refine((value) => utf8Length(value) <= max);

export const downloadManagerSchema = z.object({
  id: z.uuid(),
  kind: downloadManagerKindSchema,
  name: boundedBytes(MAX_NAME_BYTES),
  base_url: z.url().max(MAX_URL_LENGTH),
  allow_insecure: z.boolean(),
  created_at: z.iso.datetime({ offset: true }),
  updated_at: z.iso.datetime({ offset: true }),
});

export type DownloadManager = z.output<typeof downloadManagerSchema>;

export const downloadManagerInfoSchema = z.object({
  name: z.string().min(1),
  version: z.string().min(1),
  capabilities: z.object({ kinds: z.array(mediaKindSchema).min(1).max(1) }),
});

export const registeredDownloadManagerSchema = z.object({
  manager: downloadManagerSchema,
  info: downloadManagerInfoSchema,
});

export type RegisteredDownloadManager = z.output<
  typeof registeredDownloadManagerSchema
>;

export const downloadManagersPageSchema = z.object({
  items: z.array(downloadManagerSchema).max(MAX_PAGE_ITEMS),
  next_cursor: z.string().max(MAX_CURSOR_LENGTH),
});

export type DownloadManagersPage = z.output<typeof downloadManagersPageSchema>;

export const downloadManagersCursorSchema = z
  .string()
  .min(1)
  .max(MAX_CURSOR_LENGTH);

// The exact registration the server accepts. The key travels once, here.
export const registerDownloadManagerRequestSchema = z.strictObject({
  kind: downloadManagerKindSchema,
  name: boundedBytes(MAX_NAME_BYTES).refine((value) => value === value.trim()),
  base_url: z
    .url()
    .max(MAX_URL_LENGTH)
    .refine((value) => /^https?:\/\//iu.test(value)),
  api_key: z
    .string()
    .min(1)
    .max(MAX_API_KEY_LENGTH)
    .refine((value) => !/[\p{Cc}]/u.test(value)),
  allow_insecure: z.boolean(),
});

export type RegisterDownloadManagerRequest = z.output<
  typeof registerDownloadManagerRequestSchema
>;

const optionSchema = z.object({
  id: boundedBytes(MAX_OPTION_BYTES),
  name: boundedBytes(MAX_OPTION_BYTES),
});

export type DownloadManagerOption = z.output<typeof optionSchema>;

export const downloadManagerOptionsSchema = z.object({
  quality_profiles: z.array(optionSchema).max(MAX_OPTIONS),
  root_folders: z.array(optionSchema).max(MAX_OPTIONS),
  tags: z.array(optionSchema).max(MAX_OPTIONS),
});

export type DownloadManagerOptions = z.output<
  typeof downloadManagerOptionsSchema
>;

export const requestProgressSchema = z.object({
  status: z.string().max(100),
  size: z.number().int().min(0),
  size_left: z.number().int().min(0),
  estimated_completion: z.iso.datetime({ offset: true }).optional(),
});

export type RequestProgress = z.output<typeof requestProgressSchema>;

// Which media kind each manager fulfils.
export const MANAGER_KINDS: Readonly<
  Record<DownloadManagerKind, z.output<typeof mediaKindSchema>>
> = { radarr: "movie", sonarr: "series" };
