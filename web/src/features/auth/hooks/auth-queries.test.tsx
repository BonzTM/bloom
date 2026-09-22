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
  resetMockSession,
  signInMockSession,
  jsonApi,
} from "../../../mocks/handlers.js";
import { createGate } from "../../../test/gate.js";
import { server } from "../../../test/server.js";
import { AuthApi } from "../api/auth-api.js";
import { AuthApiContext } from "../auth-context.js";
import { authKeys, useLogin, useLogout, useSession } from "./auth-queries.js";

// A session-check handler that reports when it has started and then blocks
// until released, so a test can prove the request was truly in flight.
function slowSessionCheck(respond: () => Response) {
  const started = createGate();
  const release = createGate();
  server.use(
    http.get(
      "*/api/v1/auth/me",
      jsonApi(async () => {
        started.open();
        await release.wait;
        return respond();
      }),
    ),
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

// Drives the callback-style login as a promise for the tests below.
function signIn(
  login: ReturnType<typeof useLogin>,
  input: typeof mockCredentials,
): Promise<void> {
  return new Promise((resolve, reject) => {
    login.login(input, {
      onError: reject,
      onSettled: () => {
        resolve();
      },
    });
  });
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
    await signIn(result.current.login, mockCredentials);
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

it("re-checks the session when sign-in fails after the server may have acted", async () => {
  const { wrapper } = createHarness();
  server.use(
    http.post(
      "*/api/v1/auth/login",
      jsonApi(() => {
        signInMockSession();
        return Response.json({ account: { id: mockAccount.id } });
      }),
    ),
  );
  const { result } = renderHook(useAuth, { wrapper });
  await waitFor(() => {
    expect(result.current.session.data).toBeNull();
  });

  await act(async () => {
    await signIn(result.current.login, mockCredentials).catch(() => undefined);
  });

  await waitFor(() => {
    expect(result.current.session.data).toEqual(mockAccount);
  });
});

it("re-checks the session when sign-out fails after the server may have acted", async () => {
  const { wrapper } = createHarness();
  signInMockSession();
  server.use(
    http.post(
      "*/api/v1/auth/logout",
      jsonApi(() => {
        resetMockSession();
        return new Response("upstream down", { status: 502 });
      }),
    ),
  );
  const { result } = renderHook(useAuth, { wrapper });
  await waitFor(() => {
    expect(result.current.session.data).toEqual(mockAccount);
  });

  await act(async () => {
    await result.current.logout.mutateAsync().catch(() => undefined);
  });

  await waitFor(() => {
    expect(result.current.session.data).toBeNull();
  });
});

it("re-checks the session when the tab regains focus", async () => {
  const { wrapper } = createHarness();
  const { result } = renderHook(useAuth, { wrapper });
  await waitFor(() => {
    expect(result.current.session.data).toBeNull();
  });

  signInMockSession();
  // TanStack Query's focus manager listens on window, not document.
  act(() => {
    window.dispatchEvent(new Event("visibilitychange"));
  });

  await waitFor(() => {
    expect(result.current.session.data).toEqual(mockAccount);
  });
});

// Holds a request until released and reports when it started, for any path.
function heldResponse(
  method: "get" | "post",
  path: string,
  respond: () => Response,
) {
  const started = createGate();
  const release = createGate();
  server.use(
    http[method](
      path,
      jsonApi(async () => {
        started.open();
        await release.wait;
        return respond();
      }),
    ),
  );
  return { started: started.wait, release: release.open };
}

it("ignores a session check that starts during sign-in and answers after it", async () => {
  const { queryClient, wrapper } = createHarness();
  const { result } = renderHook(useAuth, { wrapper });
  await waitFor(() => {
    expect(result.current.session.data).toBeNull();
  });
  const login = heldResponse("post", "*/api/v1/auth/login", () =>
    Response.json({ account: mockAccount }),
  );
  const me = heldResponse("get", "*/api/v1/auth/me", () =>
    envelope(401, "unauthenticated", "sign in required"),
  );

  const signingIn = act(() => signIn(result.current.login, mockCredentials));
  await login.started;
  const refetch = queryClient.refetchQueries({ queryKey: authKeys.session() });
  await me.started;
  login.release();
  await signingIn;
  expect(queryClient.getQueryData(authKeys.session())).toEqual(mockAccount);

  me.release();
  await refetch;
  await waitFor(() => {
    expect(result.current.session.isFetching).toBe(false);
  });
  expect(result.current.session.data).toEqual(mockAccount);
});

it("ignores a session check that starts during sign-out and answers after it", async () => {
  const { queryClient, wrapper } = createHarness();
  signInMockSession();
  const { result } = renderHook(useAuth, { wrapper });
  await waitFor(() => {
    expect(result.current.session.data).toEqual(mockAccount);
  });
  const logout = heldResponse(
    "post",
    "*/api/v1/auth/logout",
    () => new Response(null, { status: 204 }),
  );
  const me = heldResponse("get", "*/api/v1/auth/me", () =>
    Response.json({ account: mockAccount }),
  );

  const signOut = act(() => result.current.logout.mutateAsync());
  await logout.started;
  const refetch = queryClient.refetchQueries({ queryKey: authKeys.session() });
  await me.started;
  logout.release();
  await signOut;
  expect(queryClient.getQueryData(authKeys.session())).toBeNull();

  me.release();
  await refetch;
  await waitFor(() => {
    expect(result.current.session.isFetching).toBe(false);
  });
  expect(result.current.session.data).toBeNull();
});

it("never stores the credentials in the mutation cache", async () => {
  const { queryClient, wrapper } = createHarness();
  const { result } = renderHook(useAuth, { wrapper });
  await waitFor(() => {
    expect(result.current.session.data).toBeNull();
  });
  const login = heldResponse("post", "*/api/v1/auth/login", () =>
    Response.json({ account: mockAccount }),
  );

  const signingIn = act(() => signIn(result.current.login, mockCredentials));
  await login.started;
  const [mutation] = queryClient.getMutationCache().getAll();
  expect(mutation?.state.status).toBe("pending");
  expect(mutation?.state.variables).toBeUndefined();
  expect(JSON.stringify(mutation?.state)).not.toContain("correct horse");

  login.release();
  await signingIn;
});
