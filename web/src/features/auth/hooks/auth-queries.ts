import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useRef } from "react";
import { ApiError } from "../../../lib/api/errors.js";
import type { LoginInput, Session } from "../api/auth-schemas.js";
import { useAuthApi } from "../auth-context.js";

export const authKeys = {
  session: () => ["auth", "session"] as const,
};

// The current session, or null when the visitor is signed out. A 401 from
// `/me` is the normal signed-out answer, not an error to surface. Every
// answer is compared with the cached one: when the principal changed (a
// sign-out elsewhere, a different account), data fetched for the previous
// principal is dropped before the new answer is written.
export function useSession() {
  const api = useAuthApi();
  const queryClient = useQueryClient();
  return useQuery({
    queryKey: authKeys.session(),
    queryFn: async ({ signal }): Promise<Session | null> => {
      const next = await fetchSession(api, signal);
      const previous = queryClient.getQueryData<Session | null>(
        authKeys.session(),
      );
      if (principalChanged(previous, next)) {
        forgetSessionScopedQueries(queryClient);
      }
      return next;
    },
    staleTime: 60_000,
    // The session can change outside this tab (expiry, sign-out elsewhere),
    // so coming back to the tab always re-checks it.
    refetchOnWindowFocus: "always",
  });
}

type LoginCallbacks = Readonly<{
  onError?: (error: Error) => void;
  onSettled?: () => void;
}>;

// Sign in. The credentials never become mutation variables: they sit in a ref
// only until the request is built, so the mutation cache holds nothing worth
// stealing no matter how long the request or its reconciliation takes.
export function useLogin() {
  const api = useAuthApi();
  const queryClient = useQueryClient();
  const pending = useRef<LoginInput | null>(null);
  const inFlight = useRef(false);
  const mutation = useMutation({
    mutationFn: () => {
      const input = pending.current;
      pending.current = null;
      if (input === null) {
        throw new Error("login called without credentials");
      }
      return api.login(input);
    },
    onMutate: () => cancelSessionCheck(queryClient),
    // Cancel again right before writing: a focus-triggered check may have
    // started after onMutate and must not land after the canonical answer.
    onSuccess: async (session) => {
      await cancelSessionCheck(queryClient);
      writeSession(queryClient, session);
    },
    onError: (error) => reconcileAfterAmbiguousFailure(queryClient, error),
  });
  const { mutate } = mutation;
  // The guard is synchronous: a second call while one is in flight is dropped
  // before it can overwrite the credentials the first call has not yet read.
  const login = useCallback(
    (input: LoginInput, callbacks: LoginCallbacks = {}): void => {
      if (inFlight.current) {
        return;
      }
      inFlight.current = true;
      pending.current = input;
      mutate(undefined, {
        onError: (error) => {
          callbacks.onError?.(error);
        },
        onSettled: () => {
          inFlight.current = false;
          callbacks.onSettled?.();
        },
      });
    },
    [mutate],
  );
  return { login, isPending: mutation.isPending, reset: mutation.reset };
}

// Sign out. The cache only forgets the account when the server confirms the
// session is gone (204) or says there was none (401); any other failure keeps
// the account so the UI does not claim a sign-out that did not happen.
export function useLogout() {
  const api = useAuthApi();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => api.logout(),
    onMutate: () => cancelSessionCheck(queryClient),
    onSuccess: async () => {
      await cancelSessionCheck(queryClient);
      writeSession(queryClient, null);
    },
    onError: async (error) => {
      if (isUnauthorized(error)) {
        await cancelSessionCheck(queryClient);
        writeSession(queryClient, null);
        return;
      }
      await reconcileAfterAmbiguousFailure(queryClient, error);
    },
  });
}

async function fetchSession(
  api: ReturnType<typeof useAuthApi>,
  signal: AbortSignal,
): Promise<Session | null> {
  try {
    return await api.me(signal);
  } catch (error: unknown) {
    if (isUnauthorized(error)) {
      return null;
    }
    throw error;
  }
}

// An answer that has never been cached is not a change; after that, a
// different account id, including to or from signed out, is one.
function principalChanged(
  previous: Session | null | undefined,
  next: Session | null,
): boolean {
  if (previous === undefined) {
    return false;
  }
  return (previous?.account.id ?? null) !== (next?.account.id ?? null);
}

type SessionCache = Pick<
  ReturnType<typeof useQueryClient>,
  "cancelQueries" | "setQueryData" | "invalidateQueries" | "removeQueries"
>;

// A definite rejection (4xx) means the server did nothing. Anything else, a
// malformed body, a dropped connection, a 5xx, may have changed the session
// before failing, so the truth is re-read from the server.
function reconcileAfterAmbiguousFailure(
  cache: SessionCache,
  error: unknown,
): Promise<void> | undefined {
  if (isDefiniteRejection(error)) {
    return undefined;
  }
  return cache.invalidateQueries({ queryKey: authKeys.session() });
}

function isDefiniteRejection(error: unknown): boolean {
  return (
    error instanceof ApiError &&
    error.status !== undefined &&
    error.status >= 400 &&
    error.status < 500
  );
}

function cancelSessionCheck(cache: SessionCache): Promise<void> {
  return cache.cancelQueries({ queryKey: authKeys.session() });
}

// Nothing fetched on one principal's behalf may be shown to, or reused for,
// the next one. Queries that carry the `sessionScoped` meta flag are dropped
// before the new principal is written, so no render sees both.
function forgetSessionScopedQueries(cache: SessionCache): void {
  cache.removeQueries({
    predicate: (query) => query.meta?.sessionScoped === true,
  });
}

function writeSession(cache: SessionCache, session: Session | null): void {
  forgetSessionScopedQueries(cache);
  cache.setQueryData<Session | null>(authKeys.session(), session);
}

function isUnauthorized(error: unknown): boolean {
  return error instanceof ApiError && error.status === 401;
}
