import { z } from "zod/v4";

// Wire contract for `/api/v1/request-profiles`, `/api/v1/requests`, and the
// TMDB key routes under `/api/v1/metadata/providers/tmdb/key`, mirroring
// `RequestProfile`, `RequestProfileInput`, `MediaRequest`, `RequestDecision`,
// `MetadataKeyPresence`, and `MetadataKeyRequest` in api/openapi.yaml.
// Unknown response fields are stripped so the backend can extend payloads.
const MAX_PAGE_ITEMS = 100;
const MAX_PROFILE_CURSOR = 100;
const MAX_REQUEST_CURSOR = 140;
const MAX_NAME_BYTES = 100;
const MAX_FIELD_BYTES = 500;
const MAX_TAG_BYTES = 100;
const MAX_REASON_BYTES = 1000;
const MAX_KEY_BYTES = 4096;
const MAX_SEASON = 999;

export const mediaKindSchema = z.enum(["movie", "series"]);

export type MediaKind = z.output<typeof mediaKindSchema>;

export const requestStatusSchema = z.enum([
  "pending",
  "approved",
  "declined",
  "processing",
  "available",
  "failed",
]);

export type RequestStatus = z.output<typeof requestStatusSchema>;

const boundedString = (max: number) =>
  z
    .string()
    .min(1)
    .refine((value) => utf8Length(value) <= max);

const trimmedBoundedString = (max: number) =>
  boundedString(max).refine((value) => value === value.trim());

export const requestProfileSchema = z.object({
  id: z.uuid(),
  name: boundedString(MAX_NAME_BYTES),
  kinds: z.array(mediaKindSchema),
  download_manager_kind: boundedString(MAX_FIELD_BYTES),
  download_manager_instance: boundedString(MAX_FIELD_BYTES),
  quality_profile: boundedString(MAX_FIELD_BYTES),
  root_folder: boundedString(MAX_FIELD_BYTES),
  tags: z.array(boundedString(MAX_TAG_BYTES)),
  created_at: z.iso.datetime({ offset: true }),
  updated_at: z.iso.datetime({ offset: true }),
});

export type RequestProfile = z.output<typeof requestProfileSchema>;

export const requestProfilesPageSchema = z.object({
  items: z.array(requestProfileSchema).max(MAX_PAGE_ITEMS),
  next_cursor: z.string().max(MAX_PROFILE_CURSOR),
});

export type RequestProfilesPage = z.output<typeof requestProfilesPageSchema>;

export const profilesCursorSchema = z.string().min(1).max(MAX_PROFILE_CURSOR);

// The exact request the server accepts for a profile: closed, trimmed, with
// at least one kind and no repeated kinds or tags.
export const requestProfileInputSchema = z.strictObject({
  name: trimmedBoundedString(MAX_NAME_BYTES),
  kinds: z
    .array(mediaKindSchema)
    .min(1)
    .refine((kinds) => new Set(kinds).size === kinds.length),
  download_manager_kind: trimmedBoundedString(MAX_FIELD_BYTES),
  download_manager_instance: trimmedBoundedString(MAX_FIELD_BYTES),
  quality_profile: trimmedBoundedString(MAX_FIELD_BYTES),
  root_folder: trimmedBoundedString(MAX_FIELD_BYTES),
  tags: z
    .array(trimmedBoundedString(MAX_TAG_BYTES))
    .refine((tags) => new Set(tags).size === tags.length),
});

export type RequestProfileInput = z.output<typeof requestProfileInputSchema>;

export const requestSeasonSchema = z.object({
  number: z.number().int().min(1).max(MAX_SEASON),
  status: requestStatusSchema,
});

export const mediaRequestSchema = z.object({
  id: z.uuid(),
  kind: mediaKindSchema,
  provider: z.literal("tmdb"),
  provider_id: z.string().min(1),
  title: boundedString(MAX_FIELD_BYTES),
  year: z.number().int().min(0).max(9999),
  poster_path: z
    .string()
    .refine((value) => utf8Length(value) <= MAX_FIELD_BYTES),
  requester_account_id: z.uuid(),
  profile_id: z.uuid(),
  status: requestStatusSchema,
  seasons: z.array(requestSeasonSchema),
  decision_reason: z
    .string()
    .refine((value) => utf8Length(value) <= MAX_REASON_BYTES),
  failure_reason: z
    .string()
    .refine((value) => utf8Length(value) <= MAX_REASON_BYTES),
  // What a claimed dispatch was sent with; empty until dispatched.
  download_manager_id: z.string(),
  download_manager_item_id: z.string().max(128),
  dispatch_quality_profile: z
    .string()
    .refine((value) => utf8Length(value) <= MAX_FIELD_BYTES),
  dispatch_root_folder: z
    .string()
    .refine((value) => utf8Length(value) <= MAX_FIELD_BYTES),
  dispatch_tags: z.array(boundedString(MAX_TAG_BYTES)).max(32),
  last_availability_check_at: z.iso.datetime({ offset: true }).optional(),
  decided_by_account_id: z.string(),
  decided_at: z.iso.datetime({ offset: true }).optional(),
  created_at: z.iso.datetime({ offset: true }),
  updated_at: z.iso.datetime({ offset: true }),
});

export type MediaRequest = z.output<typeof mediaRequestSchema>;

export const mediaRequestsPageSchema = z.object({
  items: z.array(mediaRequestSchema).max(MAX_PAGE_ITEMS),
  next_cursor: z.string().max(MAX_REQUEST_CURSOR),
});

export type MediaRequestsPage = z.output<typeof mediaRequestsPageSchema>;

export const requestsCursorSchema = z.string().min(1).max(MAX_REQUEST_CURSOR);

// An approval or decline may carry a reason the requester will see.
export const requestDecisionSchema = z.strictObject({
  reason: z
    .string()
    .refine((value) => value === value.trim())
    .refine((value) => utf8Length(value) <= MAX_REASON_BYTES)
    .optional(),
});

export type RequestDecision = z.output<typeof requestDecisionSchema>;

export const metadataKeyPresenceSchema = z.object({
  configured: z.boolean(),
});

export type MetadataKeyPresence = z.output<typeof metadataKeyPresenceSchema>;

export const metadataKeyRequestSchema = z.strictObject({
  api_key: z
    .string()
    .min(1)
    .max(MAX_KEY_BYTES)
    .refine((value) => utf8Length(value) <= MAX_KEY_BYTES)
    .refine((value) => !/[\p{Cc}]/u.test(value)),
});

export type MetadataKeyRequest = z.output<typeof metadataKeyRequestSchema>;

export const requestIdSchema = z.uuid();

export const requestLimits = {
  maxNameBytes: MAX_NAME_BYTES,
  maxFieldBytes: MAX_FIELD_BYTES,
  maxTagBytes: MAX_TAG_BYTES,
  maxReasonBytes: MAX_REASON_BYTES,
} as const;

export function utf8Length(value: string): number {
  return new TextEncoder().encode(value).length;
}
