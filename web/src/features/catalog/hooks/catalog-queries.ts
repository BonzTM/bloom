import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import type {
  CatalogHistoryPage,
  CatalogItemsPage,
  CatalogItemsParams,
} from "../api/catalog-schemas.js";
import { useCatalogApi } from "../catalog-context.js";

// Keys carry the account id: what one principal may see must never be served
// from another's cache. The `sessionScoped` meta flag lets the session owner
// drop these entries on sign-out.
export const catalogKeys = {
  all: ["catalog"] as const,
  libraries: (accountId: string, serverId: string) =>
    ["catalog", "libraries", accountId, serverId] as const,
  summary: (accountId: string, serverId: string, days: number | undefined) =>
    ["catalog", "summary", accountId, serverId, days ?? 0] as const,
  items: (
    accountId: string,
    serverId: string,
    libraryId: string,
    params: CatalogItemsParams,
  ) =>
    [
      "catalog",
      "items",
      accountId,
      serverId,
      libraryId,
      params.days ?? 0,
      params.itemType ?? "",
      params.archived === true,
      params.sort,
      params.order,
    ] as const,
  item: (accountId: string, serverId: string, itemId: string) =>
    ["catalog", "item", accountId, serverId, itemId] as const,
  history: (accountId: string, serverId: string, itemId: string) =>
    ["catalog", "history", accountId, serverId, itemId] as const,
  recent: (accountId: string, serverId: string, libraryId: string) =>
    ["catalog", "recent", accountId, serverId, libraryId] as const,
  genres: (
    accountId: string,
    serverId: string,
    libraryId: string,
    days: number | undefined,
  ) =>
    ["catalog", "genres", accountId, serverId, libraryId, days ?? 0] as const,
  stale: (
    accountId: string,
    serverId: string,
    libraryId: string,
    days: number,
  ) => ["catalog", "stale", accountId, serverId, libraryId, days] as const,
};

const STALE_MS = 60_000;

export function useLibraries(accountId: string, serverId: string | undefined) {
  const api = useCatalogApi();
  return useQuery({
    queryKey: catalogKeys.libraries(accountId, serverId ?? ""),
    queryFn: ({ signal }) => api.libraries(serverId ?? "", signal),
    enabled: serverId !== undefined,
    staleTime: STALE_MS,
    meta: { sessionScoped: true },
  });
}

export function useCatalogSummary(
  accountId: string,
  serverId: string | undefined,
  days: number | undefined,
) {
  const api = useCatalogApi();
  return useQuery({
    queryKey: catalogKeys.summary(accountId, serverId ?? "", days),
    queryFn: ({ signal }) => api.summary(serverId ?? "", days, signal),
    enabled: serverId !== undefined,
    staleTime: STALE_MS,
    meta: { sessionScoped: true },
  });
}

// Pages are addressed by offset; the next page starts where the last ended
// and a short page means there is no more.
function nextOffset(page: CatalogItemsPage | CatalogHistoryPage) {
  return page.items.length < page.limit ? undefined : page.offset + page.limit;
}

export function useCatalogItems(
  accountId: string,
  serverId: string,
  libraryId: string,
  params: CatalogItemsParams,
) {
  const api = useCatalogApi();
  return useInfiniteQuery({
    queryKey: catalogKeys.items(accountId, serverId, libraryId, params),
    queryFn: ({ pageParam, signal }) =>
      api.items(serverId, libraryId, params, pageParam, signal),
    initialPageParam: 0,
    getNextPageParam: nextOffset,
    staleTime: STALE_MS,
    meta: { sessionScoped: true },
  });
}

export function useCatalogItem(
  accountId: string,
  serverId: string,
  itemId: string,
) {
  const api = useCatalogApi();
  return useQuery({
    queryKey: catalogKeys.item(accountId, serverId, itemId),
    queryFn: ({ signal }) => api.item(serverId, itemId, signal),
    staleTime: STALE_MS,
    meta: { sessionScoped: true },
  });
}

export function useItemHistory(
  accountId: string,
  serverId: string,
  itemId: string,
) {
  const api = useCatalogApi();
  return useInfiniteQuery({
    queryKey: catalogKeys.history(accountId, serverId, itemId),
    queryFn: ({ pageParam, signal }) =>
      api.history(serverId, itemId, pageParam, signal),
    initialPageParam: 0,
    getNextPageParam: nextOffset,
    staleTime: STALE_MS,
    meta: { sessionScoped: true },
  });
}

export function useRecentItems(
  accountId: string,
  serverId: string,
  libraryId: string,
) {
  const api = useCatalogApi();
  return useQuery({
    queryKey: catalogKeys.recent(accountId, serverId, libraryId),
    queryFn: ({ signal }) => api.recent(serverId, libraryId, signal),
    staleTime: STALE_MS,
    meta: { sessionScoped: true },
  });
}

export function useLibraryGenres(
  accountId: string,
  serverId: string,
  libraryId: string,
  days: number | undefined,
) {
  const api = useCatalogApi();
  return useQuery({
    queryKey: catalogKeys.genres(accountId, serverId, libraryId, days),
    queryFn: ({ signal }) => api.genres(serverId, libraryId, days, signal),
    staleTime: STALE_MS,
    meta: { sessionScoped: true },
  });
}

export function useStaleItems(
  accountId: string,
  serverId: string,
  libraryId: string,
  days: number,
) {
  const api = useCatalogApi();
  return useInfiniteQuery({
    queryKey: catalogKeys.stale(accountId, serverId, libraryId, days),
    queryFn: ({ pageParam, signal }) =>
      api.stale(serverId, libraryId, days, pageParam, signal),
    initialPageParam: 0,
    getNextPageParam: nextOffset,
    staleTime: STALE_MS,
    meta: { sessionScoped: true },
  });
}

// A sync request changes nothing the page shows until the walk finishes;
// the summaries are refreshed so a finished walk appears on the next look.
export function useSyncCatalog(accountId: string) {
  const api = useCatalogApi();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (serverId: string) => api.sync(serverId),
    onSettled: () =>
      queryClient.invalidateQueries({
        queryKey: ["catalog", "summary", accountId],
      }),
  });
}
