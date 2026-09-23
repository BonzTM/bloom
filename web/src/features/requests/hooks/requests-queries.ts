import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useCallback, useRef } from "react";
import type { RequestsFilter } from "../api/requests-api.js";
import type {
  MediaRequestsPage,
  MetadataKeyRequest,
  RequestDecision,
  RequestProfileInput,
  RequestProfilesPage,
} from "../api/requests-schemas.js";
import { useRequestsApi } from "../requests-context.js";

// Keys carry the account id: what one principal may see must never be served
// from another's cache. The `sessionScoped` meta flag lets the session owner
// drop these entries on sign-out.
export const requestsKeys = {
  all: ["requests"] as const,
  profiles: (accountId: string) => ["requests", "profiles", accountId] as const,
  list: (accountId: string, filter: RequestsFilter) =>
    [
      "requests",
      "list",
      accountId,
      filter.status ?? "",
      filter.requesterId ?? "",
    ] as const,
  key: (accountId: string) => ["requests", "tmdb-key", accountId] as const,
};

const firstPage: string | undefined = undefined;

export function useRequestProfiles(accountId: string, enabled = true) {
  const api = useRequestsApi();
  return useInfiniteQuery({
    queryKey: requestsKeys.profiles(accountId),
    queryFn: ({ pageParam, signal }) => api.listProfiles(pageParam, signal),
    initialPageParam: firstPage,
    getNextPageParam: (page: RequestProfilesPage) => nextCursor(page),
    staleTime: 30_000,
    enabled,
    meta: { sessionScoped: true },
  });
}

export function useSaveProfile(accountId: string) {
  const api = useRequestsApi();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (variables: {
      id: string | undefined;
      input: RequestProfileInput;
    }) =>
      variables.id === undefined
        ? api.createProfile(variables.input)
        : api.updateProfile(variables.id, variables.input),
    onSuccess: () => invalidateProfiles(queryClient, accountId),
  });
}

export function useRemoveProfile(accountId: string) {
  const api = useRequestsApi();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.removeProfile(id),
    onSuccess: () => invalidateProfiles(queryClient, accountId),
  });
}

export function useRequests(accountId: string, filter: RequestsFilter) {
  const api = useRequestsApi();
  return useInfiniteQuery({
    queryKey: requestsKeys.list(accountId, filter),
    queryFn: ({ pageParam, signal }) =>
      api.listRequests(filter, pageParam, signal),
    initialPageParam: firstPage,
    getNextPageParam: (page: MediaRequestsPage) => nextCursor(page),
    staleTime: 15_000,
    meta: { sessionScoped: true },
  });
}

export type Decision = Readonly<{
  id: string;
  verb: "approve" | "decline";
  decision: RequestDecision;
}>;

// Approve or decline. Ids and reasons are not secrets, so they may be
// mutation variables; a success makes every request list stale.
export function useDecideRequest(accountId: string) {
  const api = useRequestsApi();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, verb, decision }: Decision) =>
      verb === "approve"
        ? api.approve(id, decision)
        : api.decline(id, decision),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: ["requests", "list", accountId],
      }),
  });
}

export function useMetadataKeyPresence(accountId: string) {
  const api = useRequestsApi();
  return useQuery({
    queryKey: requestsKeys.key(accountId),
    queryFn: ({ signal }) => api.keyPresence(signal),
    staleTime: 60_000,
    meta: { sessionScoped: true },
  });
}

// Store the TMDB key. The key never becomes a mutation variable: it sits in
// a ref only until the request is built. A second call while one is in
// flight is dropped.
export function useSetMetadataKey(accountId: string) {
  const api = useRequestsApi();
  const queryClient = useQueryClient();
  const pending = useRef<MetadataKeyRequest | null>(null);
  const inFlight = useRef(false);
  const mutation = useMutation({
    mutationFn: () => {
      const input = pending.current;
      pending.current = null;
      if (input === null) {
        throw new Error("setKey called without input");
      }
      return api.setKey(input);
    },
    gcTime: 0,
    onSuccess: (presence) => {
      queryClient.setQueryData(requestsKeys.key(accountId), presence);
    },
    onSettled: () => {
      pending.current = null;
      inFlight.current = false;
    },
  });
  const { mutate } = mutation;
  // The guard is synchronous: a second call while one is in flight is dropped
  // before it can overwrite the key the first call has not yet read.
  const setKey = useCallback(
    (input: MetadataKeyRequest, callbacks: SetKeyCallbacks = {}) => {
      if (inFlight.current) {
        return;
      }
      inFlight.current = true;
      pending.current = input;
      mutate(undefined, {
        onSuccess: () => {
          callbacks.onSuccess?.();
        },
      });
    },
    [mutate],
  );
  return {
    setKey,
    isPending: mutation.isPending,
    error: mutation.error,
    submittedAt: mutation.submittedAt,
  };
}

type SetKeyCallbacks = Readonly<{ onSuccess?: () => void }>;

export function useRemoveMetadataKey(accountId: string) {
  const api = useRequestsApi();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => api.removeKey(),
    onSuccess: () => {
      queryClient.setQueryData(requestsKeys.key(accountId), {
        configured: false,
      });
    },
  });
}

function invalidateProfiles(
  queryClient: ReturnType<typeof useQueryClient>,
  accountId: string,
): Promise<void> {
  return queryClient.invalidateQueries({
    queryKey: requestsKeys.profiles(accountId),
  });
}

function nextCursor(page: { next_cursor: string }): string | undefined {
  return page.next_cursor === "" ? undefined : page.next_cursor;
}
