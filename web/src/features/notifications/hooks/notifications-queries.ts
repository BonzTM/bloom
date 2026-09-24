import {
  useInfiniteQuery,
  useMutation,
  useQueryClient,
} from "@tanstack/react-query";
import { useCallback, useRef } from "react";
import type {
  ChannelRequest,
  ChannelsPage,
  DeliveriesPage,
  NotificationChannel,
} from "../api/notification-schemas.js";
import { useNotificationsApi } from "../notifications-context.js";

// Keys carry the account id: what one principal may see must never be served
// from another's cache. The `sessionScoped` meta flag lets the session owner
// drop these entries on sign-out.
export const notificationsKeys = {
  all: ["notifications"] as const,
  channels: (accountId: string) =>
    ["notifications", "channels", accountId] as const,
  deliveries: (accountId: string, channelId: string) =>
    ["notifications", "deliveries", accountId, channelId] as const,
};

const firstPage: string | undefined = undefined;

export function useChannels(accountId: string) {
  const api = useNotificationsApi();
  return useInfiniteQuery({
    queryKey: notificationsKeys.channels(accountId),
    queryFn: ({ pageParam, signal }) => api.list(pageParam, signal),
    initialPageParam: firstPage,
    getNextPageParam: (page: ChannelsPage) => nextCursor(page),
    staleTime: 30_000,
    meta: { sessionScoped: true },
  });
}

type SaveInput = Readonly<{ id: string | undefined; input: ChannelRequest }>;

type SaveCallbacks = Readonly<{
  onSuccess?: (channel: NotificationChannel) => void;
}>;

// Create or replace a channel. The body may carry a secret, so it never
// becomes a mutation variable: it sits in a ref only until the request is
// built, and the mutation is not kept once it settles. A second call while
// one is in flight is dropped.
export function useSaveChannel(accountId: string) {
  const api = useNotificationsApi();
  const queryClient = useQueryClient();
  const pending = useRef<SaveInput | null>(null);
  const inFlight = useRef(false);
  const mutation = useMutation({
    mutationFn: () => {
      const next = pending.current;
      pending.current = null;
      if (next === null) {
        throw new Error("saveChannel called without input");
      }
      return next.id === undefined
        ? api.create(next.input)
        : api.update(next.id, next.input);
    },
    gcTime: 0,
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: notificationsKeys.channels(accountId),
      }),
    onSettled: () => {
      pending.current = null;
      inFlight.current = false;
    },
  });
  const { mutate } = mutation;
  const save = useCallback(
    (next: SaveInput, callbacks: SaveCallbacks = {}) => {
      if (inFlight.current) {
        return;
      }
      inFlight.current = true;
      pending.current = next;
      mutate(undefined, {
        onSuccess: (channel) => {
          callbacks.onSuccess?.(channel);
        },
      });
    },
    [mutate],
  );
  return {
    save,
    isPending: mutation.isPending,
    error: mutation.error,
    submittedAt: mutation.submittedAt,
  };
}

export function useRemoveChannel(accountId: string) {
  const api = useNotificationsApi();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.remove(id),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: notificationsKeys.channels(accountId),
      }),
  });
}

// A test send changes nothing in the cache but the deliveries list.
export function useTestChannel(accountId: string) {
  const api = useNotificationsApi();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.test(id),
    onSettled: (_result, _error, id) =>
      queryClient.invalidateQueries({
        queryKey: notificationsKeys.deliveries(accountId, id),
      }),
  });
}

export function useDeliveries(accountId: string, channelId: string) {
  const api = useNotificationsApi();
  return useInfiniteQuery({
    queryKey: notificationsKeys.deliveries(accountId, channelId),
    queryFn: ({ pageParam, signal }) =>
      api.deliveries(channelId, pageParam, signal),
    initialPageParam: firstPage,
    getNextPageParam: (page: DeliveriesPage) => nextCursor(page),
    staleTime: 10_000,
    meta: { sessionScoped: true },
  });
}

function nextCursor(page: { next_cursor: string }): string | undefined {
  return page.next_cursor === "" ? undefined : page.next_cursor;
}
