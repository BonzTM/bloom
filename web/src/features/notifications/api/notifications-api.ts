import type { ApiClient } from "../../../lib/api/http-client.js";
import {
  channelIdSchema,
  channelRequestSchema,
  channelsCursorSchema,
  channelsPageSchema,
  deliveriesCursorSchema,
  deliveriesPageSchema,
  notificationChannelSchema,
  notificationPreferencesSchema,
  testResponseSchema,
  titleProviderIdSchema,
  type ChannelRequest,
  type ChannelsPage,
  type DeliveriesPage,
  type NotificationChannel,
  type NotificationPreferences,
} from "./notification-schemas.js";

const CHANNELS_PATH = "api/v1/notification-channels";
const PREFERENCES_PATH = "api/v1/me/notification-preferences";
const TITLES_PATH = "api/v1/titles";

export class NotificationsApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  // ---- the account's own preferences and title subscriptions

  preferences(signal: AbortSignal): Promise<NotificationPreferences> {
    return this.#client.requestJson(
      PREFERENCES_PATH,
      notificationPreferencesSchema,
      { signal },
    );
  }

  // Replaces the whole matrix; the server answers with what it stored.
  replacePreferences(
    input: NotificationPreferences,
  ): Promise<NotificationPreferences> {
    return this.#client.requestJson(
      PREFERENCES_PATH,
      notificationPreferencesSchema,
      { method: "PUT", body: notificationPreferencesSchema.parse(input) },
    );
  }

  subscribe(providerId: string): Promise<void> {
    return this.#client.requestEmpty(subscriptionPath(providerId), {
      method: "POST",
    });
  }

  unsubscribe(providerId: string): Promise<void> {
    return this.#client.requestEmpty(subscriptionPath(providerId), {
      method: "DELETE",
    });
  }

  list(cursor: string | undefined, signal: AbortSignal): Promise<ChannelsPage> {
    return this.#client.requestJson(
      paged(CHANNELS_PATH, cursor, channelsCursorSchema),
      channelsPageSchema,
      { signal },
    );
  }

  // Registers a channel after the server has probed it; secrets travel once
  // in this body and are never returned.
  create(input: ChannelRequest): Promise<NotificationChannel> {
    return this.#client.requestJson(CHANNELS_PATH, notificationChannelSchema, {
      method: "POST",
      body: channelRequestSchema.parse(input),
    });
  }

  // Replaces a channel; an omitted secret keeps the stored one.
  update(id: string, input: ChannelRequest): Promise<NotificationChannel> {
    return this.#client.requestJson(
      `${CHANNELS_PATH}/${segment(id)}`,
      notificationChannelSchema,
      { method: "PUT", body: channelRequestSchema.parse(input) },
    );
  }

  remove(id: string): Promise<void> {
    return this.#client.requestEmpty(`${CHANNELS_PATH}/${segment(id)}`, {
      method: "DELETE",
    });
  }

  test(id: string): Promise<void> {
    return this.#client
      .requestJson(`${CHANNELS_PATH}/${segment(id)}/test`, testResponseSchema, {
        method: "POST",
      })
      .then(() => undefined);
  }

  deliveries(
    id: string,
    cursor: string | undefined,
    signal: AbortSignal,
  ): Promise<DeliveriesPage> {
    return this.#client.requestJson(
      paged(
        `${CHANNELS_PATH}/${segment(id)}/deliveries`,
        cursor,
        deliveriesCursorSchema,
      ),
      deliveriesPageSchema,
      { signal },
    );
  }
}

function paged(
  base: string,
  cursor: string | undefined,
  cursorSchema: { parse: (value: string) => string },
): string {
  if (cursor === undefined) {
    return base;
  }
  const query = new URLSearchParams({ cursor: cursorSchema.parse(cursor) });
  return `${base}?${query.toString()}`;
}

function segment(id: string): string {
  return encodeURIComponent(channelIdSchema.parse(id));
}

function subscriptionPath(providerId: string): string {
  return `${TITLES_PATH}/tmdb/${encodeURIComponent(titleProviderIdSchema.parse(providerId))}/subscription`;
}
