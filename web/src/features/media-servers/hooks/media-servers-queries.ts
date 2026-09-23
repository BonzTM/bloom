import {
  useInfiniteQuery,
  useMutation,
  useQueryClient,
} from "@tanstack/react-query";
import { useCallback, useRef } from "react";
import type {
  MediaServersPage,
  RegisteredMediaServer,
  RegisterMediaServerInput,
} from "../api/media-servers-schemas.js";
import { useMediaServersApi } from "../media-servers-context.js";

// Keys carry the account id: what one principal may see must never be served
// from another's cache. The `sessionScoped` meta flag lets the session owner
// drop these entries on sign-out.
export const mediaServersKeys = {
  all: ["media-servers"] as const,
  list: (accountId: string) => ["media-servers", "list", accountId] as const,
};

const firstPage: string | undefined = undefined;

// Every page of registered servers fetched so far for the signed-in account.
// Without an account there is nothing to fetch.
export function useMediaServers(accountId: string | undefined) {
  const api = useMediaServersApi();
  return useInfiniteQuery({
    queryKey: mediaServersKeys.list(accountId ?? ""),
    queryFn: ({ pageParam, signal }) => api.list(pageParam, signal),
    initialPageParam: firstPage,
    getNextPageParam: nextCursor,
    enabled: accountId !== undefined,
    staleTime: 30_000,
    meta: { sessionScoped: true },
  });
}

type RegisterCallbacks = Readonly<{
  onSuccess?: (result: RegisteredMediaServer) => void;
}>;

// Register a server. The input carries the API key, so it never becomes a
// mutation variable: it sits in a ref only until the request is built, and
// the mutation cache holds nothing worth stealing for the page's lifetime.
// A success invalidates only this account's list.
export function useRegisterMediaServer(accountId: string) {
  const api = useMediaServersApi();
  const queryClient = useQueryClient();
  const pending = useRef<RegisterMediaServerInput | null>(null);
  const inFlight = useRef(false);
  const mutation = useMutation({
    mutationFn: () => {
      const input = pending.current;
      pending.current = null;
      if (input === null) {
        throw new Error("register called without input");
      }
      return api.register(input);
    },
    onSuccess: () => invalidateList(queryClient, accountId),
  });
  const { mutate } = mutation;
  // The guard is synchronous: a second call while one is in flight is dropped
  // before it can overwrite the input the first call has not yet read.
  const register = useCallback(
    (input: RegisterMediaServerInput, callbacks: RegisterCallbacks = {}) => {
      if (inFlight.current) {
        return;
      }
      inFlight.current = true;
      pending.current = input;
      mutate(undefined, {
        onSuccess: (result) => {
          callbacks.onSuccess?.(result);
        },
        onSettled: () => {
          inFlight.current = false;
        },
      });
    },
    [mutate],
  );
  return {
    register,
    isPending: mutation.isPending,
    error: mutation.error,
    submittedAt: mutation.submittedAt,
  };
}

// Remove a server by id; ids are public, so they may be mutation variables.
export function useRemoveMediaServer(accountId: string) {
  const api = useMediaServersApi();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.remove(id),
    onSuccess: () => invalidateList(queryClient, accountId),
  });
}

// The list is small, so refetching it is simpler than patching pages.
function invalidateList(
  queryClient: ReturnType<typeof useQueryClient>,
  accountId: string,
): Promise<void> {
  return queryClient.invalidateQueries({
    queryKey: mediaServersKeys.list(accountId),
  });
}

function nextCursor(lastPage: MediaServersPage): string | undefined {
  return lastPage.next_cursor === "" ? undefined : lastPage.next_cursor;
}
