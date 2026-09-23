import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import type { HistoryFilter } from "../api/playback-api.js";
import type { HistoryPage } from "../api/playback-schemas.js";
import { usePlaybackApi } from "../playback-context.js";

// Keys carry the account id: what one principal may see must never be served
// from another's cache. The `sessionScoped` meta flag lets the session owner
// drop these entries on sign-out.
export const playbackKeys = {
  all: ["playback"] as const,
  now: (accountId: string) => ["playback", "now", accountId] as const,
  history: (accountId: string, mediaServerId: string | undefined) =>
    ["playback", "history", accountId, mediaServerId ?? ""] as const,
};

// Live watches are short-lived by nature, so they are re-read on a fixed
// cadence while the page is open; nothing here is worth keeping stale.
export const NOW_PLAYING_REFRESH_MS = 10_000;

const firstPage: string | undefined = undefined;

export function useNowPlaying(accountId: string) {
  const api = usePlaybackApi();
  return useQuery({
    queryKey: playbackKeys.now(accountId),
    queryFn: ({ signal }) => api.now(signal),
    staleTime: NOW_PLAYING_REFRESH_MS / 2,
    refetchInterval: NOW_PLAYING_REFRESH_MS,
    meta: { sessionScoped: true },
  });
}

// Every page of closed watches fetched so far for the signed-in account and
// the chosen server filter.
export function usePlaybackHistory(accountId: string, filter: HistoryFilter) {
  const api = usePlaybackApi();
  return useInfiniteQuery({
    queryKey: playbackKeys.history(accountId, filter.mediaServerId),
    queryFn: ({ pageParam, signal }) => api.history(filter, pageParam, signal),
    initialPageParam: firstPage,
    getNextPageParam: nextCursor,
    staleTime: 30_000,
    meta: { sessionScoped: true },
  });
}

function nextCursor(lastPage: HistoryPage): string | undefined {
  return lastPage.next_cursor === "" ? undefined : lastPage.next_cursor;
}
