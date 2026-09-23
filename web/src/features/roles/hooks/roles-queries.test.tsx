import { expect, it, jest } from "@jest/globals";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { createGate } from "../../../test/gate.js";
import type { RolesApi } from "../api/roles-api.js";
import type { RolesPage } from "../api/roles-schemas.js";
import { RolesApiContext } from "../roles-context.js";
import { rolesKeys, useRoles } from "./roles-queries.js";

const ACCOUNT = "0b6c3d2e-1111-4a2b-9c3d-000000000001";

type Call = Readonly<{ cursor: string | undefined; signal: AbortSignal }>;

// A stand-in for the API slice that records every call and answers pages by
// cursor, so the hook is proven without HTTP.
function fakeApi(pages: Readonly<Record<string, RolesPage>>) {
  const calls: Call[] = [];
  const held = createGate();
  const api = {
    list(cursor: string | undefined, signal: AbortSignal): Promise<RolesPage> {
      calls.push({ cursor, signal });
      const page = pages[cursor ?? "first"];
      if (page === undefined) {
        return Promise.reject(new Error(`no page for ${String(cursor)}`));
      }
      return held.wait.then(() => page);
    },
  } as unknown as RolesApi;
  return { api, calls, release: held.open };
}

function harness(api: RolesApi) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  function wrapper({ children }: Readonly<{ children: ReactNode }>) {
    return (
      <QueryClientProvider client={queryClient}>
        <RolesApiContext value={api}>{children}</RolesApiContext>
      </QueryClientProvider>
    );
  }
  return { queryClient, wrapper };
}

const pages: Record<string, RolesPage> = {
  first: { items: [], next_cursor: "offset:2" },
  "offset:2": { items: [], next_cursor: "" },
};

it("asks for the first page without a cursor and the next with the server's", async () => {
  const { api, calls, release } = fakeApi(pages);
  const { wrapper } = harness(api);
  release();
  const { result } = renderHook(() => useRoles(ACCOUNT), { wrapper });
  await waitFor(() => {
    expect(result.current.status).toBe("success");
  });
  expect(calls.map((call) => call.cursor)).toEqual([undefined]);
  expect(result.current.hasNextPage).toBe(true);

  await act(async () => {
    await result.current.fetchNextPage();
  });

  expect(calls.map((call) => call.cursor)).toEqual([undefined, "offset:2"]);
  await waitFor(() => {
    expect(result.current.hasNextPage).toBe(false);
  });
});

it("cancels the request when the last observer unmounts", async () => {
  const { api, calls } = fakeApi(pages);
  const { wrapper } = harness(api);
  const { unmount } = renderHook(() => useRoles(ACCOUNT), { wrapper });
  await waitFor(() => {
    expect(calls).toHaveLength(1);
  });
  expect(calls[0]?.signal.aborted).toBe(false);

  unmount();

  await waitFor(() => {
    expect(calls[0]?.signal.aborted).toBe(true);
  });
});

it("serves a fresh page from cache and refetches once it is stale", async () => {
  const now = jest.spyOn(Date, "now");
  const start = 1_800_000_000_000;
  now.mockReturnValue(start);
  try {
    const { api, calls, release } = fakeApi(pages);
    const { wrapper } = harness(api);
    release();
    const first = renderHook(() => useRoles(ACCOUNT), { wrapper });
    await waitFor(() => {
      expect(first.result.current.status).toBe("success");
    });
    first.unmount();

    now.mockReturnValue(start + 29_000);
    const second = renderHook(() => useRoles(ACCOUNT), { wrapper });
    expect(second.result.current.status).toBe("success");
    second.unmount();
    expect(calls).toHaveLength(1);

    now.mockReturnValue(start + 31_000);
    const third = renderHook(() => useRoles(ACCOUNT), { wrapper });
    await waitFor(() => {
      expect(calls).toHaveLength(2);
    });
    third.unmount();
  } finally {
    now.mockRestore();
  }
});

it("keys the cache by account and marks it session scoped", async () => {
  const { api, release } = fakeApi(pages);
  const { queryClient, wrapper } = harness(api);
  release();
  const { result } = renderHook(() => useRoles(ACCOUNT), { wrapper });
  await waitFor(() => {
    expect(result.current.status).toBe("success");
  });

  expect(rolesKeys.list(ACCOUNT)).not.toEqual(rolesKeys.list("other"));
  const [query] = queryClient.getQueryCache().findAll({ queryKey: ["roles"] });
  expect(query?.queryKey).toEqual(["roles", "list", ACCOUNT]);
  expect(query?.meta).toEqual({ sessionScoped: true });
});

it("fetches nothing without an account", () => {
  const { api, calls } = fakeApi(pages);
  const { wrapper } = harness(api);
  const { result } = renderHook(() => useRoles(undefined), { wrapper });

  expect(result.current.fetchStatus).toBe("idle");
  expect(calls).toHaveLength(0);
});
