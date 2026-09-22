import { expect, it } from "@jest/globals";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { http } from "msw";
import type { ReactNode } from "react";
import { ApiClient } from "../../../lib/api/http-client.js";
import {
  envelope,
  mockAccount,
  mockCredentials,
  signInMockSession,
} from "../../../mocks/handlers.js";
import { server } from "../../../test/server.js";
import { AuthApi } from "../api/auth-api.js";
import { AuthApiContext } from "../auth-context.js";
import { authKeys, useLogin, useLogout, useSession } from "./auth-queries.js";

type Gate = Readonly<{ wait: Promise<void>; open: () => void }>;

// A promise the test opens by hand, so a "slow" response is held exactly as
// long as the test needs and never depends on the wall clock.
function createGate(): Gate {
  let open = (): void => undefined;
  const wait = new Promise<void>((resolve) => {
    open = resolve;
  });
  return { wait, open };
}

// A session-check handler that reports when it has started and then blocks
// until released, so a test can prove the request was truly in flight.
function slowSessionCheck(respond: () => Response) {
  const started = createGate();
  const release = createGate();
  server.use(
    http.get("*/api/v1/auth/me", async () => {
      started.open();
      await release.wait;
      return respond();
    }),
  );
  return { started: started.wait, release: release.open };
}

function createHarness() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: Number.POSITIVE_INFINITY },
      mutations: { retry: false },
    },
  });
  const api = new AuthApi(new ApiClient(new URL("http://localhost/")));
  function wrapper({ children }: Readonly<{ children: ReactNode }>) {
    return (
      <QueryClientProvider client={queryClient}>
        <AuthApiContext value={api}>{children}</AuthApiContext>
      </QueryClientProvider>
    );
  }
  return { queryClient, wrapper };
}

function useAuth() {
  return { session: useSession(), login: useLogin(), logout: useLogout() };
}

it("keeps the signed-in account when a session check started earlier answers late", async () => {
  const { queryClient, wrapper } = createHarness();
  const slow = slowSessionCheck(() =>
    envelope(401, "unauthenticated", "sign in required"),
  );
  const { result } = renderHook(useAuth, { wrapper });
  await slow.started;
  expect(result.current.session.isPending).toBe(true);

  await act(async () => {
    await result.current.login.mutateAsync(mockCredentials);
  });
  expect(queryClient.getQueryData(authKeys.session())).toEqual(mockAccount);

  slow.release();
  await waitFor(() => {
    expect(result.current.session.isFetching).toBe(false);
  });
  expect(result.current.session.data).toEqual(mockAccount);
});

it("stays signed out when a session check started before sign-out answers late", async () => {
  const { queryClient, wrapper } = createHarness();
  signInMockSession();
  const { result } = renderHook(useAuth, { wrapper });
  await waitFor(() => {
    expect(result.current.session.data).toEqual(mockAccount);
  });

  const slow = slowSessionCheck(() => Response.json({ account: mockAccount }));
  const refetch = queryClient.refetchQueries({ queryKey: authKeys.session() });
  await slow.started;
  await act(async () => {
    await result.current.logout.mutateAsync();
  });
  expect(queryClient.getQueryData(authKeys.session())).toBeNull();

  slow.release();
  await refetch;
  await waitFor(() => {
    expect(result.current.session.isFetching).toBe(false);
  });
  expect(result.current.session.data).toBeNull();
});
