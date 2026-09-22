import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../../../lib/api/errors.js";
import type { Account, LoginInput } from "../api/auth-schemas.js";
import { useAuthApi } from "../auth-context.js";

export const authKeys = {
  all: ["auth"] as const,
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
// observes it, and callers reset it after it settles.
export function useLogin() {
  const api = useAuthApi();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: LoginInput) => api.login(input),
    gcTime: 0,
    onMutate: async () => {
      // A `/me` answer that started before the login must not overwrite the
      // account the login returns.
      await queryClient.cancelQueries({ queryKey: authKeys.session() });
    },
    onSuccess: (response) => {
      queryClient.setQueryData<Account | null>(
        authKeys.session(),
        response.account,
      );
    },
  });
}

// Sign out. The cache only forgets the account when the server confirms the
// session is gone (204) or says there was none (401); any other failure keeps
// the account so the UI does not claim a sign-out that did not happen.
export function useLogout() {
  const api = useAuthApi();
  const queryClient = useQueryClient();
  const forget = (): void => {
    queryClient.setQueryData<Account | null>(authKeys.session(), null);
  };
  return useMutation({
    mutationFn: () => api.logout(),
    onSuccess: forget,
    onError: (error) => {
      if (isUnauthorized(error)) {
        forget();
      }
    },
  });
}

export function isUnauthorized(error: unknown): boolean {
  return error instanceof ApiError && error.status === 401;
}
