import type { ApiClient } from "../../../lib/api/http-client.js";
import {
  channelIdSchema,
  channelRequestSchema,
  channelsCursorSchema,
  channelsPageSchema,
  deliveriesCursorSchema,
  deliveriesPageSchema,
  notificationChannelSchema,
  testResponseSchema,
  type ChannelRequest,
  type ChannelsPage,
  type DeliveriesPage,
  type NotificationChannel,
} from "./notification-schemas.js";

const CHANNELS_PATH = "api/v1/notification-channels";

export class NotificationsApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
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
