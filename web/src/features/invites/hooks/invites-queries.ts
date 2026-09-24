import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useCallback, useRef } from "react";
import {
  INVITE_CODE_PATTERN,
  type AcceptedInvite,
  type AcceptInviteRequest,
  type CreatedInvite,
  type CreateInviteRequest,
} from "../api/invites-schemas.js";
import { useInvitesApi } from "../invites-context.js";

// Admin keys carry the account id: what one principal may see must never be
// served from another's cache, and the `sessionScoped` meta flag lets the
// session owner drop these entries on sign-out. The public preview is keyed
// by code alone: it is the same answer for everyone.
export const invitesKeys = {
  all: ["invites"] as const,
  list: (accountId: string) => ["invites", "list", accountId] as const,
  servers: (accountId: string) => ["invites", "servers", accountId] as const,
  preview: (code: string) => ["invites", "preview", code] as const,
};

const firstPage: string | undefined = undefined;

export function useInvites(accountId: string) {
  const api = useInvitesApi();
  return useInfiniteQuery({
    queryKey: invitesKeys.list(accountId),
    queryFn: ({ pageParam, signal }) => api.list(pageParam, signal),
    initialPageParam: firstPage,
    getNextPageParam: nextCursor,
    staleTime: 30_000,
    meta: { sessionScoped: true },
  });
}

export function useInviteServers(accountId: string) {
  const api = useInvitesApi();
  return useInfiniteQuery({
    queryKey: invitesKeys.servers(accountId),
    queryFn: ({ pageParam, signal }) => api.servers(pageParam, signal),
    initialPageParam: firstPage,
    getNextPageParam: nextCursor,
    staleTime: 30_000,
    meta: { sessionScoped: true },
  });
}

type CreateCallbacks = Readonly<{
  onCreated?: (created: CreatedInvite) => void;
}>;

// Create an invite. The answer carries the code, which is shown once and
// must not linger: the mutation resolves to nothing, the answer is handed
// to the caller from hook-owned callbacks that run even after the page has
// unmounted, and every ref is cleared once the mutation settles. The
// mutation itself is not kept once it is unobserved.
export function useCreateInvite(accountId: string) {
  const api = useInvitesApi();
  const queryClient = useQueryClient();
  const handoff = useRef<CreatedInvite | null>(null);
  const onCreated = useRef<CreateCallbacks["onCreated"]>(undefined);
  const inFlight = useRef(false);
  const mutation = useMutation({
    mutationFn: async (input: CreateInviteRequest): Promise<void> => {
      handoff.current = await api.create(input);
    },
    gcTime: 0,
    onSuccess: async () => {
      const created = handoff.current;
      handoff.current = null;
      if (created !== null) {
        onCreated.current?.(created);
      }
      await invalidateList(queryClient, accountId);
    },
    onSettled: () => {
      handoff.current = null;
      onCreated.current = undefined;
      inFlight.current = false;
    },
  });
  const { mutate } = mutation;
  // The guard is synchronous: a second call while one is in flight is dropped
  // before it can replace the callback that the first call's code needs.
  const create = useCallback(
    (input: CreateInviteRequest, callbacks: CreateCallbacks = {}) => {
      if (inFlight.current) {
        return;
      }
      inFlight.current = true;
      onCreated.current = callbacks.onCreated;
      mutate(input);
    },
    [mutate],
  );
  return {
    create,
    isPending: mutation.isPending,
    error: mutation.error,
    submittedAt: mutation.submittedAt,
  };
}

// Revoke an invite by id; ids are public, so they may be mutation variables.
export function useRevokeInvite(accountId: string) {
  const api = useInvitesApi();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.revoke(id),
    onSuccess: () => invalidateList(queryClient, accountId),
  });
}

// What a visitor may learn about an invite. A code that cannot be valid is
// never sent; a 404 is the final answer, so there is nothing to retry. The
// answer is dropped as soon as nothing shows it: the code is the key.
export function useInvitePreview(code: string) {
  const api = useInvitesApi();
  return useQuery({
    queryKey: invitesKeys.preview(code),
    queryFn: ({ signal }) => api.preview(code, signal),
    enabled: INVITE_CODE_PATTERN.test(code),
    retry: false,
    staleTime: 60_000,
    gcTime: 0,
  });
}

export function forgetInvitePreview(
  queryClient: ReturnType<typeof useQueryClient>,
  code: string,
): void {
  queryClient.removeQueries({ queryKey: invitesKeys.preview(code) });
}

type AcceptInput = Readonly<{ code: string; request: AcceptInviteRequest }>;

type AcceptCallbacks = Readonly<{
  onAccepted?: (accepted: AcceptedInvite) => void;
}>;

// Accept an invite. The code and the chosen password never become mutation
// variables or closures kept by the mutation: they sit in a ref only until
// the request is built, and the caller's callback lives in a hook-owned ref
// that is cleared when the mutation settles. A second call while one is in
// flight is dropped.
export function useAcceptInvite() {
  const api = useInvitesApi();
  const pending = useRef<AcceptInput | null>(null);
  const onAccepted = useRef<AcceptCallbacks["onAccepted"]>(undefined);
  const inFlight = useRef(false);
  const mutation = useMutation({
    mutationFn: () => {
      const input = pending.current;
      pending.current = null;
      if (input === null) {
        throw new Error("accept called without input");
      }
      return api.accept(input.code, input.request);
    },
    gcTime: 0,
    onSuccess: (accepted) => {
      onAccepted.current?.(accepted);
    },
    onSettled: () => {
      pending.current = null;
      onAccepted.current = undefined;
      inFlight.current = false;
    },
  });
  const { mutate } = mutation;
  const accept = useCallback(
    (input: AcceptInput, callbacks: AcceptCallbacks = {}) => {
      if (inFlight.current) {
        return;
      }
      inFlight.current = true;
      pending.current = input;
      onAccepted.current = callbacks.onAccepted;
      mutate(undefined);
    },
    [mutate],
  );
  return { accept, isPending: mutation.isPending, error: mutation.error };
}

function invalidateList(
  queryClient: ReturnType<typeof useQueryClient>,
  accountId: string,
): Promise<void> {
  return queryClient.invalidateQueries({
    queryKey: invitesKeys.list(accountId),
  });
}

function nextCursor(lastPage: { next_cursor: string }): string | undefined {
  return lastPage.next_cursor === "" ? undefined : lastPage.next_cursor;
}
