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
  mockSession,
  resetMockSession,
  signInMockSession,
  jsonApi,
} from "../../../mocks/handlers.js";
import { createGate } from "../../../test/gate.js";
import { server } from "../../../test/server.js";
import { AuthApi } from "../api/auth-api.js";
import { AuthApiContext } from "../auth-context.js";
import { authKeys, useLogin, useLogout, useSession } from "./auth-queries.js";

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
  const slow = heldResponse("get", "*/api/v1/auth/me", () =>
    envelope(401, "unauthorized", "sign in required"),
  );
  const { result } = renderHook(useAuth, { wrapper });
  await slow.started;
  expect(result.current.session.isPending).toBe(true);

  await act(async () => {
    await signIn(result.current.login, mockCredentials);
  });
  expect(queryClient.getQueryData(authKeys.session())).toEqual(mockSession);

  slow.release();
  await waitFor(() => {
    expect(result.current.session.isFetching).toBe(false);
  });
  expect(result.current.session.data).toEqual(mockSession);
});

it("stays signed out when a session check started before sign-out answers late", async () => {
  const { queryClient, wrapper } = createHarness();
  signInMockSession();
  const { result } = renderHook(useAuth, { wrapper });
  await waitFor(() => {
    expect(result.current.session.data).toEqual(mockSession);
  });

  const slow = heldResponse("get", "*/api/v1/auth/me", () =>
    Response.json(mockSession),
  );
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
    expect(result.current.session.data).toEqual(mockSession);
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
    expect(result.current.session.data).toEqual(mockSession);
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
    expect(result.current.session.data).toEqual(mockSession);
  });
});

it("ignores a session check that starts during sign-in and answers after it", async () => {
  const { queryClient, wrapper } = createHarness();
  const { result } = renderHook(useAuth, { wrapper });
  await waitFor(() => {
    expect(result.current.session.data).toBeNull();
  });
  const login = heldResponse("post", "*/api/v1/auth/login", () =>
    Response.json(mockSession),
  );
  const me = heldResponse("get", "*/api/v1/auth/me", () =>
    envelope(401, "unauthorized", "sign in required"),
  );

  const signingIn = act(() => signIn(result.current.login, mockCredentials));
  await login.started;
  const refetch = queryClient.refetchQueries({ queryKey: authKeys.session() });
  await me.started;
  login.release();
  await signingIn;
  expect(queryClient.getQueryData(authKeys.session())).toEqual(mockSession);

  me.release();
  await refetch;
  await waitFor(() => {
    expect(result.current.session.isFetching).toBe(false);
  });
  expect(result.current.session.data).toEqual(mockSession);
});

it("ignores a session check that starts during sign-out and answers after it", async () => {
  const { queryClient, wrapper } = createHarness();
  signInMockSession();
  const { result } = renderHook(useAuth, { wrapper });
  await waitFor(() => {
    expect(result.current.session.data).toEqual(mockSession);
  });
  const logout = heldResponse(
    "post",
    "*/api/v1/auth/logout",
    () => new Response(null, { status: 204 }),
  );
  const me = heldResponse("get", "*/api/v1/auth/me", () =>
    Response.json(mockSession),
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
    Response.json(mockSession),
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

it("drops a second sign-in while the first is in flight", async () => {
  const { wrapper } = createHarness();
  const { result } = renderHook(useAuth, { wrapper });
  await waitFor(() => {
    expect(result.current.session.data).toBeNull();
  });
  let requests = 0;
  const login = heldResponse("post", "*/api/v1/auth/login", () => {
    requests += 1;
    return Response.json(mockSession);
  });

  let first: Promise<void> = Promise.resolve();
  let secondSettled = false;
  await act(async () => {
    first = signIn(result.current.login, mockCredentials);
    result.current.login.login(
      { username: "other", password: "other" },
      {
        onSettled: () => {
          secondSettled = true;
        },
      },
    );
    await login.started;
  });
  login.release();
  await act(async () => {
    await first;
  });

  expect(requests).toBe(1);
  expect(secondSettled).toBe(false);
  await waitFor(() => {
    expect(result.current.session.data).toEqual(mockSession);
  });
});

// Data fetched for one principal must never survive into another's session.
function scopedQuery(queryClient: QueryClient, key: string, scoped: boolean) {
  queryClient.setQueryData([key], "cached");
  const query = queryClient.getQueryCache().find({ queryKey: [key] });
  if (query === undefined) {
    throw new Error("query not cached");
  }
  query.setOptions({
    queryKey: [key],
    meta: scoped ? { sessionScoped: true } : {},
  });
}

function cachedKeys(queryClient: QueryClient): string[] {
  return queryClient
    .getQueryCache()
    .getAll()
    .map((query) => String(query.queryKey[0]))
    .sort();
}

it("drops session-scoped data when a re-check finds the person signed out", async () => {
  const { queryClient, wrapper } = createHarness();
  signInMockSession();
  const { result } = renderHook(useAuth, { wrapper });
  await waitFor(() => {
    expect(result.current.session.data).toEqual(mockSession);
  });
  scopedQuery(queryClient, "roles", true);
  scopedQuery(queryClient, "version", false);

  resetMockSession();
  await queryClient.refetchQueries({ queryKey: authKeys.session() });

  await waitFor(() => {
    expect(result.current.session.data).toBeNull();
  });
  expect(cachedKeys(queryClient)).toEqual(["auth", "version"]);
});

it("drops session-scoped data when a re-check finds a different account", async () => {
  const { queryClient, wrapper } = createHarness();
  signInMockSession();
  const { result } = renderHook(useAuth, { wrapper });
  await waitFor(() => {
    expect(result.current.session.data).toEqual(mockSession);
  });
  scopedQuery(queryClient, "roles", true);
  const other = {
    ...mockSession,
    account: { ...mockAccount, id: "0b6c3d2e-1111-4a2b-9c3d-000000000002" },
  };
  server.use(
    http.get(
      "*/api/v1/auth/me",
      jsonApi(() => Response.json(other)),
    ),
  );

  await queryClient.refetchQueries({ queryKey: authKeys.session() });

  await waitFor(() => {
    expect(result.current.session.data).toEqual(other);
  });
  expect(cachedKeys(queryClient)).toEqual(["auth"]);
});

it("keeps session-scoped data when a re-check confirms the same account", async () => {
  const { queryClient, wrapper } = createHarness();
  signInMockSession();
  const { result } = renderHook(useAuth, { wrapper });
  await waitFor(() => {
    expect(result.current.session.data).toEqual(mockSession);
  });
  scopedQuery(queryClient, "roles", true);

  await queryClient.refetchQueries({ queryKey: authKeys.session() });

  expect(cachedKeys(queryClient)).toEqual(["auth", "roles"]);
});

it("keeps session-scoped data when a re-check is cancelled", async () => {
  const { queryClient, wrapper } = createHarness();
  signInMockSession();
  const { result } = renderHook(useAuth, { wrapper });
  await waitFor(() => {
    expect(result.current.session.data).toEqual(mockSession);
  });
  scopedQuery(queryClient, "roles", true);
  const held = heldResponse("get", "*/api/v1/auth/me", () =>
    envelope(401, "unauthorized", "sign in required"),
  );

  const refetch = queryClient.refetchQueries({ queryKey: authKeys.session() });
  await held.started;
  await queryClient.cancelQueries({ queryKey: authKeys.session() });
  held.release();
  await refetch;

  expect(result.current.session.data).toEqual(mockSession);
  expect(cachedKeys(queryClient)).toEqual(["auth", "roles"]);
});

it("removes session-scoped data before the new principal is written", async () => {
  const { queryClient, wrapper } = createHarness();
  signInMockSession();
  const { result } = renderHook(useAuth, { wrapper });
  await waitFor(() => {
    expect(result.current.session.data).toEqual(mockSession);
  });
  scopedQuery(queryClient, "roles", true);
  let scopedAtWrite: number | undefined;
  const sessionQuery = queryClient
    .getQueryCache()
    .find({ queryKey: authKeys.session() });
  const unsubscribe = queryClient.getQueryCache().subscribe((event) => {
    if (
      event.type === "updated" &&
      event.action.type === "success" &&
      event.query === sessionQuery
    ) {
      scopedAtWrite = queryClient.getQueryCache().findAll({
        predicate: (query) => query.meta?.sessionScoped === true,
      }).length;
    }
  });

  resetMockSession();
  await queryClient.refetchQueries({ queryKey: authKeys.session() });
  unsubscribe();

  expect(scopedAtWrite).toBe(0);
});
