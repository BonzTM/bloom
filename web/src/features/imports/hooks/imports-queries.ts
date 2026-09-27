import {
  useInfiniteQuery,
  useMutation,
  useQueryClient,
  type QueryClient,
} from "@tanstack/react-query";
import type {
  ImportJob,
  ImportsPage,
  StartImportInput,
} from "../api/imports-schemas.js";
import { useImportsApi } from "../imports-context.js";

// Keys carry the account id: what one principal may see must never be served
// from another's cache. The `sessionScoped` meta flag lets the session owner
// drop these entries on sign-out.
export const importsKeys = {
  all: ["imports"] as const,
  list: (accountId: string) => ["imports", "list", accountId] as const,
};

const firstPage: string | undefined = undefined;

// Jobs refresh every few seconds while one is pending or running, so the
// counters move without a reload; idle lists refresh on the usual schedule.
const ACTIVE_REFETCH_MS = 5_000;

export function isActive(job: ImportJob): boolean {
  return job.state === "pending" || job.state === "running";
}

export function useImports(accountId: string) {
  const api = useImportsApi();
  return useInfiniteQuery({
    queryKey: importsKeys.list(accountId),
    queryFn: ({ pageParam, signal }) => api.list(pageParam, signal),
    initialPageParam: firstPage,
    getNextPageParam: (page: ImportsPage) =>
      page.next_cursor === "" ? undefined : page.next_cursor,
    staleTime: 30_000,
    refetchInterval: (query) =>
      (query.state.data?.pages ?? []).some((page) => page.items.some(isActive))
        ? ACTIVE_REFETCH_MS
        : false,
    meta: { sessionScoped: true },
  });
}

// The input carries no secret, so it may be a mutation variable. A success
// invalidates only this account's list.
export function useStartImport(accountId: string) {
  const api = useImportsApi();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: StartImportInput) => api.start(input),
    onSuccess: () => invalidateList(queryClient, accountId),
  });
}

export function useCancelImport(accountId: string) {
  const api = useImportsApi();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.cancel(id),
    onSuccess: () => invalidateList(queryClient, accountId),
  });
}

function invalidateList(
  queryClient: QueryClient,
  accountId: string,
): Promise<void> {
  return queryClient.invalidateQueries({
    queryKey: importsKeys.list(accountId),
  });
}
