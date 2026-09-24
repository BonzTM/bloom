import { z } from "zod/v4";

// Wire contract for `/api/v1/notification-channels`, mirroring
// `NotificationChannel`, `NotificationChannelRequest` and its per-kind
// parts, `NotificationDelivery`, and `NotificationTestResponse` in
// api/openapi.yaml. Unknown response fields are stripped.
const MAX_PAGE_ITEMS = 100;
const MAX_CHANNEL_CURSOR = 400;
const MAX_DELIVERY_CURSOR = 256;
const MAX_NAME_BYTES = 100;
const MAX_TEMPLATE_BYTES = 4096;
const MAX_URL_LENGTH = 2048;
const MAX_SECRET_LENGTH = 4096;
const MAX_HOST_LENGTH = 253;
const MAX_ADDRESS_LENGTH = 320;
const MAX_FROM_NAME_LENGTH = 200;
const MAX_RECIPIENTS = 32;

export const notificationKindSchema = z.enum(["webhook", "discord", "email"]);

export type NotificationKind = z.output<typeof notificationKindSchema>;

export const notificationEventTypeSchema = z.enum([
  "created",
  "approved",
  "declined",
  "dispatched",
  "available",
  "failed",
]);

export type NotificationEventType = z.output<
  typeof notificationEventTypeSchema
>;

export const tlsModeSchema = z.enum(["starttls", "implicit"]);
export const authModeSchema = z.enum(["plain", "login"]);

function utf8Length(value: string): number {
  return new TextEncoder().encode(value).length;
}

const boundedBytes = (max: number, min = 0) =>
  z
    .string()
    .min(min)
    .refine((value) => utf8Length(value) <= max);

const noControls = (value: string) => !/[\p{Cc}]/u.test(value);

export const subscriptionsSchema = z
  .array(notificationEventTypeSchema)
  .min(1)
  .max(6)
  .refine((items) => new Set(items).size === items.length);

export const credentialPresenceSchema = z.object({
  url_set: z.boolean(),
  shared_secret_set: z.boolean(),
  password_set: z.boolean(),
});

export const notificationChannelSchema = z.object({
  id: z.uuid(),
  kind: notificationKindSchema,
  name: boundedBytes(MAX_NAME_BYTES, 1),
  enabled: z.boolean(),
  subscriptions: subscriptionsSchema,
  subject_template: boundedBytes(MAX_TEMPLATE_BYTES),
  body_template: boundedBytes(MAX_TEMPLATE_BYTES),
  allow_insecure: z.boolean(),
  allow_private: z.boolean(),
  smtp_host: z.string().max(MAX_HOST_LENGTH).optional(),
  smtp_port: z.number().int().min(1).max(65_535).optional(),
  tls_mode: tlsModeSchema.optional(),
  auth_mode: authModeSchema.optional(),
  username: z.string().max(MAX_ADDRESS_LENGTH).optional(),
  from_address: z.string().max(MAX_ADDRESS_LENGTH).optional(),
  from_name: z.string().max(MAX_FROM_NAME_LENGTH).optional(),
  recipients: z
    .array(z.string().max(MAX_ADDRESS_LENGTH))
    .max(MAX_RECIPIENTS)
    .optional(),
  credentials: credentialPresenceSchema,
  consecutive_failures: z.number().int().min(0),
  created_at: z.iso.datetime({ offset: true }),
  updated_at: z.iso.datetime({ offset: true }),
});

export type NotificationChannel = z.output<typeof notificationChannelSchema>;

export const channelsPageSchema = z.object({
  items: z.array(notificationChannelSchema).max(MAX_PAGE_ITEMS),
  next_cursor: z.string().max(MAX_CHANNEL_CURSOR),
});

export type ChannelsPage = z.output<typeof channelsPageSchema>;

export const channelsCursorSchema = z.string().min(1).max(MAX_CHANNEL_CURSOR);

// ---- requests. Secrets are optional on the wire so an edit can keep the
// stored value by omitting them; a create requires them, which the form
// input parser enforces.
const httpUrl = z
  .url()
  .max(MAX_URL_LENGTH)
  .refine((value) => /^https?:\/\//iu.test(value));

const secret = z.string().min(1).max(MAX_SECRET_LENGTH).refine(noControls);

export const webhookRequestSchema = z.strictObject({
  url: httpUrl.optional(),
  shared_secret: secret.optional(),
  allow_insecure: z.boolean(),
  allow_private: z.boolean(),
});

export const discordRequestSchema = z.strictObject({
  webhook_url: httpUrl.optional(),
  allow_insecure: z.boolean(),
  allow_private: z.boolean(),
});

const emailAddress = z.email().max(MAX_ADDRESS_LENGTH);

export const emailRequestSchema = z.strictObject({
  smtp_host: z.string().min(1).max(MAX_HOST_LENGTH).refine(noControls),
  smtp_port: z.number().int().min(1).max(65_535),
  tls_mode: tlsModeSchema,
  auth_mode: authModeSchema,
  username: z.string().min(1).max(MAX_ADDRESS_LENGTH).refine(noControls),
  password: secret.optional(),
  from_address: emailAddress,
  from_name: z.string().max(MAX_FROM_NAME_LENGTH).refine(noControls),
  recipients: z
    .array(emailAddress)
    .min(1)
    .max(MAX_RECIPIENTS)
    .refine((items) => new Set(items).size === items.length),
  allow_private: z.boolean(),
});

const templateText = boundedBytes(MAX_TEMPLATE_BYTES);

export const channelRequestSchema = z
  .strictObject({
    kind: notificationKindSchema,
    name: boundedBytes(MAX_NAME_BYTES, 1).refine(
      (value) => value === value.trim() && noControls(value),
    ),
    enabled: z.boolean(),
    subscriptions: subscriptionsSchema,
    subject_template: templateText,
    body_template: templateText,
    webhook: webhookRequestSchema.optional(),
    discord: discordRequestSchema.optional(),
    email: emailRequestSchema.optional(),
  })
  .refine(
    (input) =>
      [input.webhook, input.discord, input.email].filter(
        (part) => part !== undefined,
      ).length === 1,
  )
  .refine((input) => input[input.kind] !== undefined);

export type ChannelRequest = z.output<typeof channelRequestSchema>;

export const deliverySchema = z.object({
  id: z.uuid(),
  event_type: notificationEventTypeSchema,
  request_id: z.uuid(),
  status: z.enum(["pending", "sent", "failed"]),
  attempts: z.number().int().min(0).max(8),
  last_error: z.string().max(512),
  next_attempt_at: z.iso.datetime({ offset: true }),
  sent_at: z.iso.datetime({ offset: true }).optional(),
  created_at: z.iso.datetime({ offset: true }),
  updated_at: z.iso.datetime({ offset: true }),
});

export type Delivery = z.output<typeof deliverySchema>;

export const deliveriesPageSchema = z.object({
  items: z.array(deliverySchema).max(MAX_PAGE_ITEMS),
  next_cursor: z.string().max(MAX_DELIVERY_CURSOR),
});

export type DeliveriesPage = z.output<typeof deliveriesPageSchema>;

export const deliveriesCursorSchema = z
  .string()
  .min(1)
  .max(MAX_DELIVERY_CURSOR);

export const testResponseSchema = z.object({ outcome: z.literal("sent") });

export const channelIdSchema = z.uuid();

export const notificationLimits = {
  maxNameBytes: MAX_NAME_BYTES,
  maxTemplateBytes: MAX_TEMPLATE_BYTES,
  maxRecipients: MAX_RECIPIENTS,
} as const;
