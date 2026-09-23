import { useInfiniteQuery } from "@tanstack/react-query";
import type { RolesPage } from "../api/roles-schemas.js";
import { useRolesApi } from "../roles-context.js";

// Keys carry the account id: what one principal may see must never be served
// from another's cache. The `sessionScoped` meta flag lets the session owner
// drop these entries on sign-out.
export const rolesKeys = {
  all: ["roles"] as const,
  list: (accountId: string) => ["roles", "list", accountId] as const,
};

const firstPage: string | undefined = undefined;

// Every page of roles fetched so far for the signed-in account. Pages are
// appended as the person asks for more; an empty `next_cursor` from the
// server ends the list. Without an account there is nothing to fetch.
export function useRoles(accountId: string | undefined) {
  const api = useRolesApi();
  return useInfiniteQuery({
    queryKey: rolesKeys.list(accountId ?? ""),
    queryFn: ({ pageParam, signal }) => api.list(pageParam, signal),
    initialPageParam: firstPage,
    getNextPageParam: nextCursor,
    enabled: accountId !== undefined,
    staleTime: 30_000,
    meta: { sessionScoped: true },
  });
}

function nextCursor(lastPage: RolesPage): string | undefined {
  return lastPage.next_cursor === "" ? undefined : lastPage.next_cursor;
}
