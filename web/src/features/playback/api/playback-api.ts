import type { ApiClient } from "../../../lib/api/http-client.js";
import {
  historyCursorSchema,
  historyPageSchema,
  mediaServerIdSchema,
  nowPlayingSchema,
  type HistoryPage,
  type NowPlaying,
} from "./playback-schemas.js";

const NOW_PATH = "api/v1/playback/now";
const HISTORY_PATH = "api/v1/playback/history";

export type HistoryFilter = Readonly<{ mediaServerId?: string }>;

export class PlaybackApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  // Every open watch across every server, newest first.
  now(signal: AbortSignal): Promise<NowPlaying> {
    return this.#client.requestJson(NOW_PATH, nowPlayingSchema, { signal });
  }

  // One page of closed watches, newest first, optionally for one server.
  // `cursor` is the previous page's `next_cursor`; omit it for the first page.
  history(
    filter: HistoryFilter,
    cursor: string | undefined,
    signal: AbortSignal,
  ): Promise<HistoryPage> {
    return this.#client.requestJson(
      historyPath(filter, cursor),
      historyPageSchema,
      { signal },
    );
  }
}

function historyPath(
  filter: HistoryFilter,
  cursor: string | undefined,
): string {
  const query = new URLSearchParams();
  if (filter.mediaServerId !== undefined) {
    query.set(
      "media_server_id",
      mediaServerIdSchema.parse(filter.mediaServerId),
    );
  }
  if (cursor !== undefined) {
    query.set("cursor", historyCursorSchema.parse(cursor));
  }
  const encoded = query.toString();
  return encoded === "" ? HISTORY_PATH : `${HISTORY_PATH}?${encoded}`;
}
