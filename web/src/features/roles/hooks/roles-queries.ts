import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { ApiError } from "../../../lib/api/errors.js";
import type { RequestQuota, RequestQuotaInput } from "../api/quota-schemas.js";
import type { RolesPage } from "../api/roles-schemas.js";
import { useRolesApi } from "../roles-context.js";

// Keys carry the account id: what one principal may see must never be served
// from another's cache. The `sessionScoped` meta flag lets the session owner
// drop these entries on sign-out.
export const rolesKeys = {
  all: ["roles"] as const,
  list: (accountId: string) => ["roles", "list", accountId] as const,
  quota: (accountId: string, roleId: string) =>
    ["roles", "quota", accountId, roleId] as const,
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

// A role's request quota, read only while something shows it. A 404 is the
// answer "no quota", not a failure, so it resolves to null.
export function useRoleQuota(accountId: string, roleId: string) {
  const api = useRolesApi();
  return useQuery({
    queryKey: rolesKeys.quota(accountId, roleId),
    queryFn: async ({ signal }): Promise<RequestQuota | null> => {
      try {
        return await api.quota(roleId, signal);
      } catch (error: unknown) {
        if (error instanceof ApiError && error.status === 404) {
          return null;
        }
        throw error;
      }
    },
    staleTime: 30_000,
    meta: { sessionScoped: true },
  });
}

export function useSetRoleQuota(accountId: string, roleId: string) {
  const api = useRolesApi();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: RequestQuotaInput) => api.setQuota(roleId, input),
    onSuccess: (quota) => {
      queryClient.setQueryData(rolesKeys.quota(accountId, roleId), quota);
    },
  });
}

export function useRemoveRoleQuota(accountId: string, roleId: string) {
  const api = useRolesApi();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => api.removeQuota(roleId),
    onSuccess: () => {
      queryClient.setQueryData(rolesKeys.quota(accountId, roleId), null);
    },
  });
}

function nextCursor(lastPage: RolesPage): string | undefined {
  return lastPage.next_cursor === "" ? undefined : lastPage.next_cursor;
}
