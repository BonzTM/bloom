import { z } from "zod/v4";
import type { ApiClient } from "../../../lib/api/http-client.js";
import {
  accountMediaUserSchema,
  accountMediaUsersSchema,
  linkMediaUserRequestSchema,
  mediaServerUsersSchema,
  mediaUserIdSchema,
  statsDailySchema,
  statsDaysSchema,
  statsLibrariesSchema,
  statsLibraryIdSchema,
  statsOverviewSchema,
  statsPatternsSchema,
  statsTitleKindSchema,
  statsTitleOrderSchema,
  statsTitlesSchema,
  statsUserDetailSchema,
  statsUsersSchema,
  statsZoneSchema,
  type AccountMediaUser,
  type AccountMediaUsers,
  type MediaServerUsers,
  type StatsDaily,
  type StatsLibraries,
  type StatsOverview,
  type StatsParams,
  type StatsPatterns,
  type StatsTitleKind,
  type StatsTitleOrder,
  type StatsTitles,
  type StatsUserDetail,
  type StatsUsers,
} from "./stats-schemas.js";
import {
  activityCursorSchema,
  activityFilterSchema,
  activityPageSchema,
  historyCursorSchema,
  historyPageSchema,
  mediaServerIdSchema,
  nowPlayingSchema,
  playbackPositionsSchema,
  timelineGapSchema,
  timelinePageSchema,
  timelineUserIdSchema,
  watchIdSchema,
  type ActivityFilter,
  type ActivityPage,
  type HistoryPage,
  type NowPlaying,
  type PlaybackPositions,
  type TimelinePage,
} from "./playback-schemas.js";

const NOW_PATH = "api/v1/playback/now";
const ACTIVITY_PATH = "api/v1/activity";
const MEDIA_SERVERS_PATH = "api/v1/media-servers";
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

  // Every watch across servers and people that matches the filter, newest
  // first, one keyset page at a time (stats.read.all).
  activity(
    filter: ActivityFilter,
    cursor: string | undefined,
    signal: AbortSignal,
  ): Promise<ActivityPage> {
    return this.#client.requestJson(
      activityPath(filter, cursor),
      activityPageSchema,
      { signal },
    );
  }

  // One person's watches on one server grouped into sittings: plays of the
  // same item closer together than the gap fold into one entry.
  timeline(
    serverId: string,
    mediaUserId: string,
    gapSeconds: number,
    cursor: string | undefined,
    signal: AbortSignal,
  ): Promise<TimelinePage> {
    return this.#client.requestJson(
      timelinePath(serverId, mediaUserId, gapSeconds, cursor),
      timelinePageSchema,
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
    order: StatsTitleOrder,
    signal: AbortSignal,
  ): Promise<StatsTitles> {
    return this.#client.requestJson(
      statsPath("titles", params, {
        kind: statsTitleKindSchema.parse(kind),
        order: statsTitleOrderSchema.parse(order),
      }),
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

  // A media server's users (admin.settings), for choosing whom to link.
  mediaServerUsers(
    mediaServerId: string,
    signal: AbortSignal,
  ): Promise<MediaServerUsers> {
    const id = encodeURIComponent(mediaServerIdSchema.parse(mediaServerId));
    return this.#client.requestJson(
      `api/v1/media-servers/${id}/users`,
      mediaServerUsersSchema,
      { signal },
    );
  }

  // Link one Bloom account to a media user on one server (admin.settings).
  linkMediaUser(
    accountId: string,
    mediaServerId: string,
    mediaUserId: string,
  ): Promise<AccountMediaUser> {
    const account = encodeURIComponent(z.uuid().parse(accountId));
    const server = encodeURIComponent(mediaServerIdSchema.parse(mediaServerId));
    return this.#client.requestJson(
      `api/v1/accounts/${account}/media-users/${server}`,
      accountMediaUserSchema,
      {
        method: "PUT",
        body: linkMediaUserRequestSchema.parse({ media_user_id: mediaUserId }),
      },
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

const ACTIVITY_QUERY_NAMES: readonly (readonly [
  keyof ActivityFilter,
  string,
])[] = [
  ["mediaServerId", "media_server_id"],
  ["q", "q"],
  ["playMethod", "play_method"],
  ["source", "source"],
  ["startedAfter", "started_after"],
  ["startedBefore", "started_before"],
];

function activityPath(
  filter: ActivityFilter,
  cursor: string | undefined,
): string {
  const valid = activityFilterSchema.parse(filter);
  const query = new URLSearchParams();
  for (const [key, name] of ACTIVITY_QUERY_NAMES) {
    const value = valid[key];
    if (value !== undefined) {
      query.set(name, value);
    }
  }
  if (cursor !== undefined) {
    query.set("cursor", activityCursorSchema.parse(cursor));
  }
  const encoded = query.toString();
  return encoded === "" ? ACTIVITY_PATH : `${ACTIVITY_PATH}?${encoded}`;
}

function timelinePath(
  serverId: string,
  mediaUserId: string,
  gapSeconds: number,
  cursor: string | undefined,
): string {
  const query = new URLSearchParams({
    gap_seconds: String(timelineGapSchema.parse(gapSeconds)),
  });
  if (cursor !== undefined) {
    query.set("cursor", historyCursorSchema.parse(cursor));
  }
  const server = encodeURIComponent(mediaServerIdSchema.parse(serverId));
  const user = encodeURIComponent(timelineUserIdSchema.parse(mediaUserId));
  return `${MEDIA_SERVERS_PATH}/${server}/users/${user}/timeline?${query.toString()}`;
}
