import type { ApiClient } from "../../../lib/api/http-client.js";
import {
  mediaUserIdSchema,
  statsDailySchema,
  accountMediaUsersSchema,
  statsDaysSchema,
  statsLibrariesSchema,
  statsLibraryIdSchema,
  statsOverviewSchema,
  statsPatternsSchema,
  statsTitleKindSchema,
  statsTitlesSchema,
  statsUserDetailSchema,
  statsUsersSchema,
  statsZoneSchema,
  type AccountMediaUsers,
  type StatsDaily,
  type StatsLibraries,
  type StatsOverview,
  type StatsParams,
  type StatsPatterns,
  type StatsTitleKind,
  type StatsTitles,
  type StatsUserDetail,
  type StatsUsers,
} from "./stats-schemas.js";
import {
  historyCursorSchema,
  historyPageSchema,
  mediaServerIdSchema,
  nowPlayingSchema,
  playbackPositionsSchema,
  watchIdSchema,
  type HistoryPage,
  type NowPlaying,
  type PlaybackPositions,
} from "./playback-schemas.js";

const NOW_PATH = "api/v1/playback/now";
const HISTORY_PATH = "api/v1/playback/history";
const STATS_PATH = "api/v1/stats";

export type HistoryFilter = Readonly<{ mediaServerId?: string }>;

export class PlaybackApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  // Every open watch across every server, newest first.
  // The bounded sample series of one watch, newest first.
  positions(watchId: string, signal: AbortSignal): Promise<PlaybackPositions> {
    const id = encodeURIComponent(watchIdSchema.parse(watchId));
    return this.#client.requestJson(
      `api/v1/playback/watches/${id}/positions`,
      playbackPositionsSchema,
      { signal },
    );
  }

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

  // ---- statistics (stats.read.all)

  statsOverview(
    params: StatsParams,
    signal: AbortSignal,
  ): Promise<StatsOverview> {
    return this.#client.requestJson(
      statsPath("overview", params),
      statsOverviewSchema,
      { signal },
    );
  }

  statsDaily(params: StatsParams, signal: AbortSignal): Promise<StatsDaily> {
    return this.#client.requestJson(
      statsPath("daily", params),
      statsDailySchema,
      {
        signal,
      },
    );
  }

  statsPatterns(
    params: StatsParams,
    signal: AbortSignal,
  ): Promise<StatsPatterns> {
    return this.#client.requestJson(
      statsPath("patterns", params),
      statsPatternsSchema,
      { signal },
    );
  }

  statsTitles(
    params: StatsParams,
    kind: StatsTitleKind,
    signal: AbortSignal,
  ): Promise<StatsTitles> {
    return this.#client.requestJson(
      statsPath("titles", params, { kind: statsTitleKindSchema.parse(kind) }),
      statsTitlesSchema,
      { signal },
    );
  }

  statsLibraries(
    params: StatsParams,
    signal: AbortSignal,
  ): Promise<StatsLibraries> {
    return this.#client.requestJson(
      statsPath("libraries", params),
      statsLibrariesSchema,
      { signal },
    );
  }

  statsUsers(params: StatsParams, signal: AbortSignal): Promise<StatsUsers> {
    return this.#client.requestJson(
      statsPath("users", params),
      statsUsersSchema,
      {
        signal,
      },
    );
  }

  // The caller's own dashboard through the media user linked to the
  // account, and the links themselves (stats.read.own).
  statsMe(params: StatsParams, signal: AbortSignal): Promise<StatsUserDetail> {
    return this.#client.requestJson(
      statsPath("me", params),
      statsUserDetailSchema,
      { signal },
    );
  }

  myMediaUsers(signal: AbortSignal): Promise<AccountMediaUsers> {
    return this.#client.requestJson(
      "api/v1/me/media-users",
      accountMediaUsersSchema,
      { signal },
    );
  }

  statsUser(
    params: StatsParams,
    mediaServerId: string,
    mediaUserId: string,
    signal: AbortSignal,
  ): Promise<StatsUserDetail> {
    const segment = `users/${encodeURIComponent(mediaServerIdSchema.parse(mediaServerId))}/${encodeURIComponent(mediaUserIdSchema.parse(mediaUserId))}`;
    return this.#client.requestJson(
      statsPath(segment, params),
      statsUserDetailSchema,
      { signal },
    );
  }
}

function statsPath(
  report: string,
  params: StatsParams,
  extra: Readonly<Record<string, string>> = {},
): string {
  const query = new URLSearchParams(extra);
  query.set("days", String(statsDaysSchema.parse(params.days)));
  query.set("tz", statsZoneSchema.parse(params.timeZone));
  if (params.mediaServerId !== undefined) {
    query.set(
      "media_server_id",
      mediaServerIdSchema.parse(params.mediaServerId),
    );
  }
  if (params.libraryId !== undefined) {
    if (params.mediaServerId === undefined) {
      throw new Error("a library filter needs its media server");
    }
    query.set("library_id", statsLibraryIdSchema.parse(params.libraryId));
  }
  return `${STATS_PATH}/${report}?${query.toString()}`;
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
