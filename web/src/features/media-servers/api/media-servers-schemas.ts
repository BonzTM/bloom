import { z } from "zod/v4";

// Wire contract for `/api/v1/media-servers`, mirroring `MediaServer`,
// `MediaServersResponse`, `CreateMediaServerRequest`, and
// `CreateMediaServerResponse` in api/openapi.yaml. Unknown response fields
// are stripped so the backend can extend payloads without breaking the UI.
const MAX_PAGE_ITEMS = 100;
const MAX_CURSOR_LENGTH = 400;
const MAX_NAME_BYTES = 100;
const MAX_URL_BYTES = 2048;
const MAX_API_KEY_BYTES = 4096;

export const mediaServerKindSchema = z.enum(["jellyfin"]);

export type MediaServerKind = z.output<typeof mediaServerKindSchema>;

export const capabilitiesSchema = z.object({
  create_user_with_password: z.boolean(),
  set_password: z.boolean(),
  quick_connect_approval: z.boolean(),
  provider_id_lookup: z.boolean(),
});

export type Capabilities = z.output<typeof capabilitiesSchema>;

export const mediaServerSchema = z.object({
  id: z.uuid(),
  kind: mediaServerKindSchema,
  name: z
    .string()
    .min(1)
    .refine((value) => utf8Length(value) <= MAX_NAME_BYTES),
  base_url: z
    .url({ protocol: /^https?$/u })
    .refine((value) => utf8Length(value) <= MAX_URL_BYTES),
  allow_insecure: z.boolean(),
  created_at: z.iso.datetime({ offset: true }),
  updated_at: z.iso.datetime({ offset: true }),
  capabilities: capabilitiesSchema,
});

export type MediaServer = z.output<typeof mediaServerSchema>;

export const serverInfoSchema = z.object({
  name: z.string().min(1),
  version: z.string().min(1),
  id: z.string().min(1),
});

export type ServerInfo = z.output<typeof serverInfoSchema>;

// An empty `next_cursor` marks the final page.
export const mediaServersPageSchema = z.object({
  items: z.array(mediaServerSchema).max(MAX_PAGE_ITEMS),
  next_cursor: z.string().max(MAX_CURSOR_LENGTH),
});

export type MediaServersPage = z.output<typeof mediaServersPageSchema>;

export const mediaServersCursorSchema = z
  .string()
  .min(1)
  .max(MAX_CURSOR_LENGTH);

export const registeredMediaServerSchema = z.object({
  server: mediaServerSchema,
  info: serverInfoSchema,
});

export type RegisteredMediaServer = z.output<
  typeof registeredMediaServerSchema
>;

// The bounds the server enforces, checked here so a person hears about a
// problem before the request leaves the browser. Sizes are UTF-8 bytes, as
// the contract measures them.
export const registerMediaServerInputSchema = z.object({
  kind: mediaServerKindSchema,
  name: z
    .string()
    .trim()
    .min(1, "Enter a name for this server.")
    .refine(
      (value) => utf8Length(value) <= MAX_NAME_BYTES,
      `Use a name of at most ${String(MAX_NAME_BYTES)} bytes.`,
    ),
  base_url: z
    .string()
    .trim()
    .min(1, "Enter the server address.")
    .regex(/^https?:\/\/\S+$/u, "Enter an http:// or https:// address.")
    .refine(
      (value) => utf8Length(value) <= MAX_URL_BYTES,
      `Use an address of at most ${String(MAX_URL_BYTES)} bytes.`,
    ),
  api_key: z
    .string()
    .min(1, "Enter the API key.")
    .refine(
      (value) => utf8Length(value) <= MAX_API_KEY_BYTES,
      `Use an API key of at most ${String(MAX_API_KEY_BYTES)} bytes.`,
    )
    .refine(
      (value) => !/[\p{Cc}]/u.test(value),
      "The API key must not contain control characters.",
    ),
  allow_insecure: z.boolean().default(false),
});

export type RegisterMediaServerInput = z.output<
  typeof registerMediaServerInputSchema
>;

// The exact request the server accepts: `CreateMediaServerRequest` is closed
// (unknown fields are rejected), the address is a complete http(s) URL, and
// the name is already trimmed. The API slice parses every request through
// it, and the mock server accepts nothing looser.
export const registerMediaServerRequestSchema = z.strictObject({
  kind: mediaServerKindSchema,
  name: z
    .string()
    .min(1)
    .refine((value) => value === value.trim())
    .refine((value) => utf8Length(value) <= MAX_NAME_BYTES),
  base_url: z
    .url({ protocol: /^https?$/u })
    .refine((value) => utf8Length(value) <= MAX_URL_BYTES),
  api_key: z
    .string()
    .min(1)
    .refine((value) => utf8Length(value) <= MAX_API_KEY_BYTES)
    .refine((value) => !/[\p{Cc}]/u.test(value)),
  allow_insecure: z.boolean().default(false),
});

export type RegisterMediaServerRequest = z.output<
  typeof registerMediaServerRequestSchema
>;

export const mediaServerIdSchema = z.uuid();

function utf8Length(value: string): number {
  return new TextEncoder().encode(value).length;
}
