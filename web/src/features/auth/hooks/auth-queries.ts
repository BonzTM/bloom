import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useRef } from "react";
import { ApiError } from "../../../lib/api/errors.js";
import type { Account, LoginInput } from "../api/auth-schemas.js";
import { useAuthApi } from "../auth-context.js";

export const authKeys = {
  session: () => ["auth", "session"] as const,
};

// The current session, or null when the visitor is signed out. A 401 from
// `/me` is the normal signed-out answer, not an error to surface.
export function useSession() {
  const api = useAuthApi();
  return useQuery({
    queryKey: authKeys.session(),
    queryFn: async ({ signal }): Promise<Account | null> => {
      try {
        const response = await api.me(signal);
        return response.account;
      } catch (error: unknown) {
        if (isUnauthorized(error)) {
          return null;
        }
        throw error;
      }
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
    onSuccess: async (response) => {
      await cancelSessionCheck(queryClient);
      writeSession(queryClient, response.account);
    },
    onError: (error) => reconcileAfterAmbiguousFailure(queryClient, error),
  });
  const { mutate } = mutation;
  const login = useCallback(
    (input: LoginInput, callbacks: LoginCallbacks = {}): void => {
      pending.current = input;
      mutate(undefined, callbacks);
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

type SessionCache = Pick<
  ReturnType<typeof useQueryClient>,
  "cancelQueries" | "setQueryData" | "invalidateQueries"
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

function writeSession(cache: SessionCache, account: Account | null): void {
  cache.setQueryData<Account | null>(authKeys.session(), account);
}

function isUnauthorized(error: unknown): boolean {
  return error instanceof ApiError && error.status === 401;
}
