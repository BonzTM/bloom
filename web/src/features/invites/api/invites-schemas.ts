import { z } from "zod/v4";

// Wire contract for `/api/v1/invites` and the public `/api/v1/invite/{code}`
// routes, mirroring `Invite`, `CreateInviteRequest`, `CreateInviteResponse`,
// `InvitesResponse`, `PublicInviteResponse`, `AcceptInviteRequest`, and
// `AcceptInviteResponse` in api/openapi.yaml. Unknown response fields are
// stripped so the backend can extend payloads without breaking the UI.
const MAX_PAGE_ITEMS = 100;
const MAX_CURSOR_LENGTH = 128;
const MAX_LABEL_BYTES = 100;
const MAX_USES = 1000;
const MAX_USERNAME_BYTES = 64;
const MIN_PASSWORD_CHARACTERS = 15;
const MAX_PASSWORD_CHARACTERS = 1024;
const MAX_PASSWORD_BYTES = 4096;

export const INVITE_CODE_PATTERN = /^[A-Z2-7]{26}$/u;

export const inviteCodeSchema = z.string().regex(INVITE_CODE_PATTERN);

export const inviteStatusSchema = z.enum([
  "active",
  "expired",
  "exhausted",
  "revoked",
]);

export type InviteStatus = z.output<typeof inviteStatusSchema>;

// Library ids are opaque server strings of at most 128 bytes, at most 256 of
// them, each at most once. Empty or omitted means every library on the server.
const MAX_LIBRARY_IDS = 256;
const MAX_LIBRARY_ID_BYTES = 128;

export const libraryIdsSchema = z
  .array(
    z
      .string()
      .min(1)
      .refine((id) => utf8Length(id) <= MAX_LIBRARY_ID_BYTES),
  )
  .max(MAX_LIBRARY_IDS)
  .refine((ids) => new Set(ids).size === ids.length);

export const inviteSchema = z.object({
  id: z.uuid(),
  media_server_id: z.uuid(),
  label: z
    .string()
    .min(1)
    .refine((value) => utf8Length(value) <= MAX_LABEL_BYTES),
  expires_at: z.iso.datetime({ offset: true }).optional(),
  max_uses: z.number().int().min(1).max(MAX_USES).optional(),
  use_count: z.number().int().min(0),
  library_ids: libraryIdsSchema,
  status: inviteStatusSchema,
  created_at: z.iso.datetime({ offset: true }),
});

export type Invite = z.output<typeof inviteSchema>;

// An empty `next_cursor` marks the final page.
export const invitesPageSchema = z.object({
  items: z.array(inviteSchema).max(MAX_PAGE_ITEMS),
  next_cursor: z.string().max(MAX_CURSOR_LENGTH),
});

export type InvitesPage = z.output<typeof invitesPageSchema>;

export const invitesCursorSchema = z.string().min(1).max(MAX_CURSOR_LENGTH);

// The exact request the server accepts: `CreateInviteRequest` is closed and
// the label is already trimmed. Omitting `library_ids` grants every library.
export const createInviteRequestSchema = z.strictObject({
  media_server_id: z.uuid(),
  label: z
    .string()
    .min(1)
    .refine((value) => value === value.trim())
    .refine((value) => utf8Length(value) <= MAX_LABEL_BYTES),
  expires_at: z.iso.datetime({ offset: true }).optional(),
  max_uses: z.number().int().min(1).max(MAX_USES).optional(),
  library_ids: libraryIdsSchema.optional(),
});

export type CreateInviteRequest = z.output<typeof createInviteRequestSchema>;

// The code comes back exactly once; only its digest is kept server-side.
export const createdInviteSchema = z.object({
  invite: inviteSchema,
  code: inviteCodeSchema,
  accept_path: z.string().regex(/^\/invite\/[A-Z2-7]{26}$/u),
});

export type CreatedInvite = z.output<typeof createdInviteSchema>;

export const publicInviteSchema = z.object({
  media_server_name: z.string().min(1),
  username_rule: z.string().min(1),
  password_rule: z.string().min(1),
});

export type PublicInvite = z.output<typeof publicInviteSchema>;

export const acceptInviteRequestSchema = z.strictObject({
  username: z
    .string()
    .min(1)
    .refine((value) => value === value.trim())
    .refine((value) => utf8Length(value) <= MAX_USERNAME_BYTES)
    .refine((value) => !/[\p{Cc}]/u.test(value)),
  password: z
    .string()
    .min(MIN_PASSWORD_CHARACTERS)
    .max(MAX_PASSWORD_CHARACTERS)
    .refine((value) => utf8Length(value) <= MAX_PASSWORD_BYTES),
});

export type AcceptInviteRequest = z.output<typeof acceptInviteRequestSchema>;

export const acceptedInviteSchema = z.object({
  media_server_name: z.string().min(1),
  username: z
    .string()
    .min(1)
    .refine((value) => utf8Length(value) <= MAX_USERNAME_BYTES),
});

export type AcceptedInvite = z.output<typeof acceptedInviteSchema>;

export const inviteIdSchema = z.uuid();

export const inviteLimits = {
  maxLabelBytes: MAX_LABEL_BYTES,
  maxUses: MAX_USES,
  maxUsernameBytes: MAX_USERNAME_BYTES,
  minPasswordCharacters: MIN_PASSWORD_CHARACTERS,
  maxPasswordCharacters: MAX_PASSWORD_CHARACTERS,
} as const;

export function utf8Length(value: string): number {
  return new TextEncoder().encode(value).length;
}
