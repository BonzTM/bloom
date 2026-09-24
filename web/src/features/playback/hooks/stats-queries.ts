import { useQuery } from "@tanstack/react-query";
import type { StatsParams, StatsTitleKind } from "../api/stats-schemas.js";
import { usePlaybackApi } from "../playback-context.js";

// Keys carry the account id and every parameter the report was computed
// for; the `sessionScoped` meta flag lets the session owner drop these
// entries on sign-out.
export const statsKeys = {
  all: ["stats"] as const,
  report: (
    accountId: string,
    report: string,
    params: StatsParams,
    extra = "",
  ) =>
    [
      "stats",
      report,
      accountId,
      params.days,
      params.mediaServerId ?? "",
      params.libraryId ?? "",
      params.timeZone,
      extra,
    ] as const,
};

const STALE_MS = 30_000;

export function useStatsOverview(accountId: string, params: StatsParams) {
  const api = usePlaybackApi();
  return useQuery({
    queryKey: statsKeys.report(accountId, "overview", params),
    queryFn: ({ signal }) => api.statsOverview(params, signal),
    staleTime: STALE_MS,
    meta: { sessionScoped: true },
  });
}

export function useStatsDaily(accountId: string, params: StatsParams) {
  const api = usePlaybackApi();
  return useQuery({
    queryKey: statsKeys.report(accountId, "daily", params),
    queryFn: ({ signal }) => api.statsDaily(params, signal),
    staleTime: STALE_MS,
    meta: { sessionScoped: true },
  });
}

export function useStatsPatterns(accountId: string, params: StatsParams) {
  const api = usePlaybackApi();
  return useQuery({
    queryKey: statsKeys.report(accountId, "patterns", params),
    queryFn: ({ signal }) => api.statsPatterns(params, signal),
    staleTime: STALE_MS,
    meta: { sessionScoped: true },
  });
}

export function useStatsTitles(
  accountId: string,
  params: StatsParams,
  kind: StatsTitleKind,
) {
  const api = usePlaybackApi();
  return useQuery({
    queryKey: statsKeys.report(accountId, "titles", params, kind),
    queryFn: ({ signal }) => api.statsTitles(params, kind, signal),
    staleTime: STALE_MS,
    meta: { sessionScoped: true },
  });
}

export function useStatsLibraries(accountId: string, params: StatsParams) {
  const api = usePlaybackApi();
  return useQuery({
    queryKey: statsKeys.report(accountId, "libraries", params),
    queryFn: ({ signal }) => api.statsLibraries(params, signal),
    staleTime: STALE_MS,
    meta: { sessionScoped: true },
  });
}

export function useStatsUsers(accountId: string, params: StatsParams) {
  const api = usePlaybackApi();
  return useQuery({
    queryKey: statsKeys.report(accountId, "users", params),
    queryFn: ({ signal }) => api.statsUsers(params, signal),
    staleTime: STALE_MS,
    meta: { sessionScoped: true },
  });
}

export function useStatsMe(accountId: string, params: StatsParams) {
  const api = usePlaybackApi();
  return useQuery({
    queryKey: statsKeys.report(accountId, "me", params),
    queryFn: ({ signal }) => api.statsMe(params, signal),
    staleTime: STALE_MS,
    meta: { sessionScoped: true },
  });
}

export function useMyMediaUsers(accountId: string) {
  const api = usePlaybackApi();
  return useQuery({
    queryKey: ["stats", "me", "media-users", accountId] as const,
    queryFn: ({ signal }) => api.myMediaUsers(signal),
    staleTime: STALE_MS,
    meta: { sessionScoped: true },
  });
}

export function useStatsUser(
  accountId: string,
  params: StatsParams,
  mediaServerId: string,
  mediaUserId: string,
) {
  const api = usePlaybackApi();
  return useQuery({
    queryKey: statsKeys.report(
      accountId,
      "user",
      params,
      `${mediaServerId}/${mediaUserId}`,
    ),
    queryFn: ({ signal }) =>
      api.statsUser(params, mediaServerId, mediaUserId, signal),
    staleTime: STALE_MS,
    meta: { sessionScoped: true },
  });
}
