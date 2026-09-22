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

export function useLogin() {
  const api = useAuthApi();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: LoginInput) => api.login(input),
    onSuccess: (response) => {
      queryClient.setQueryData<Account | null>(
        authKeys.session(),
        response.account,
      );
    },
  });
}

export function useLogout() {
  const api = useAuthApi();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => api.logout(),
    onSettled: async () => {
      queryClient.setQueryData<Account | null>(authKeys.session(), null);
      await queryClient.invalidateQueries({ queryKey: authKeys.all });
    },
  });
}

function isUnauthorized(error: unknown): boolean {
  return error instanceof ApiError && error.status === 401;
}
