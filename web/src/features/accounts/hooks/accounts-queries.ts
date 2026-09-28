import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useAccountsApi } from "../accounts-context.js";
import type { AdminAccountsPage } from "../api/accounts-schemas.js";

// Keys carry the viewer's account id so one administrator's cache is never
// served to another principal; `sessionScoped` lets sign-out drop them.
export const accountKeys = {
  all: ["accounts"] as const,
  list: (viewerId: string, search: string) =>
    ["accounts", "list", viewerId, search] as const,
  account: (viewerId: string, accountId: string) =>
    ["accounts", "account", viewerId, accountId] as const,
};

const STALE_MS = 30_000;

function nextCursor(page: AdminAccountsPage): string | undefined {
  return page.next_cursor === "" ? undefined : page.next_cursor;
}

const firstPage: string | undefined = undefined;

export function useAccounts(viewerId: string, search: string) {
  const api = useAccountsApi();
  return useInfiniteQuery({
    queryKey: accountKeys.list(viewerId, search),
    queryFn: ({ pageParam, signal }) => api.list(search, pageParam, signal),
    initialPageParam: firstPage,
    getNextPageParam: nextCursor,
    staleTime: STALE_MS,
    meta: { sessionScoped: true },
  });
}

export function useAccount(viewerId: string, accountId: string) {
  const api = useAccountsApi();
  return useQuery({
    queryKey: accountKeys.account(viewerId, accountId),
    queryFn: ({ signal }) => api.get(accountId, signal),
    staleTime: STALE_MS,
    meta: { sessionScoped: true },
  });
}
