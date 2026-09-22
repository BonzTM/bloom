import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
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
  });
}

// Sign in. The password is a mutation variable only for the duration of the
// request: `gcTime: 0` drops the mutation from the cache as soon as nothing
// observes it, and callers reset it after it settles. An in-flight session
// check is cancelled first so a late answer cannot overwrite the result.
export function useLogin() {
  const api = useAuthApi();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: LoginInput) => api.login(input),
    gcTime: 0,
    onMutate: () => cancelSessionCheck(queryClient),
    onSuccess: (response) => {
      writeSession(queryClient, response.account);
    },
  });
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
    onSuccess: () => {
      writeSession(queryClient, null);
    },
    onError: (error) => {
      if (isUnauthorized(error)) {
        writeSession(queryClient, null);
      }
    },
  });
}

type SessionCache = Pick<
  ReturnType<typeof useQueryClient>,
  "cancelQueries" | "setQueryData"
>;

function cancelSessionCheck(cache: SessionCache): Promise<void> {
  return cache.cancelQueries({ queryKey: authKeys.session() });
}

function writeSession(cache: SessionCache, account: Account | null): void {
  cache.setQueryData<Account | null>(authKeys.session(), account);
}

function isUnauthorized(error: unknown): boolean {
  return error instanceof ApiError && error.status === 401;
}
