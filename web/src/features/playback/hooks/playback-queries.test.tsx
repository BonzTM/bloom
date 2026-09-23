import { beforeEach, expect, it } from "@jest/globals";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { signInMockSession } from "../../../mocks/handlers.js";
import { ApiClient } from "../../../lib/api/http-client.js";
import { PlaybackApi } from "../api/playback-api.js";
import { PlaybackApiContext } from "../playback-context.js";
import {
  NOW_PLAYING_REFRESH_MS,
  playbackKeys,
  useNowPlaying,
  usePlaybackHistory,
} from "./playback-queries.js";

const ACCOUNT = "0b6c3d2e-1111-4a2b-9c3d-000000000001";
const OTHER_ACCOUNT = "0b6c3d2e-1111-4a2b-9c3d-000000000002";
const LIVING_ROOM = "3d7f1a2b-0000-4000-8000-000000000002";

function harness() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: Number.POSITIVE_INFINITY },
    },
  });
  const api = new PlaybackApi(new ApiClient(new URL("http://localhost/")));
  function wrapper({ children }: Readonly<{ children: ReactNode }>): ReactNode {
    return (
      <QueryClientProvider client={queryClient}>
        <PlaybackApiContext value={api}>{children}</PlaybackApiContext>
      </QueryClientProvider>
    );
  }
  return { queryClient, wrapper };
}

beforeEach(() => {
  signInMockSession();
});

it("re-reads what is playing on the refresh interval only while mounted", async () => {
  const { queryClient, wrapper } = harness();
  const { result, unmount } = renderHook(() => useNowPlaying(ACCOUNT), {
    wrapper,
  });
  await waitFor(() => {
    expect(result.current.status).toBe("success");
  });
  const query = queryClient.getQueryCache().find({
    queryKey: playbackKeys.now(ACCOUNT),
  });
  expect(query?.observers[0]?.options.refetchInterval).toBe(
    NOW_PLAYING_REFRESH_MS,
  );
  expect(query?.meta).toEqual({ sessionScoped: true });

  unmount();

  // No observer, no interval: TanStack clears the timer with the last one.
  expect(query?.getObserversCount()).toBe(0);
});

it("keeps accounts and filters apart and drops them all on sign-out", async () => {
  const { queryClient, wrapper } = harness();
  const { result } = renderHook(
    () => ({
      mine: usePlaybackHistory(ACCOUNT, {}),
      filtered: usePlaybackHistory(ACCOUNT, { mediaServerId: LIVING_ROOM }),
      theirs: usePlaybackHistory(OTHER_ACCOUNT, {}),
    }),
    { wrapper },
  );
  await waitFor(() => {
    expect(result.current.theirs.status).toBe("success");
    expect(result.current.filtered.status).toBe("success");
    expect(result.current.mine.status).toBe("success");
  });
  const keys = queryClient
    .getQueryCache()
    .getAll()
    .map((query) => JSON.stringify(query.queryKey));
  expect(keys).toContain(
    JSON.stringify(playbackKeys.history(ACCOUNT, undefined)),
  );
  expect(keys).toContain(
    JSON.stringify(playbackKeys.history(ACCOUNT, LIVING_ROOM)),
  );
  expect(keys).toContain(
    JSON.stringify(playbackKeys.history(OTHER_ACCOUNT, undefined)),
  );
  expect(result.current.filtered.data?.pages[0]?.items).toHaveLength(1);
  expect(result.current.mine.data?.pages[0]?.items).toHaveLength(2);

  // What the session owner does on a principal change.
  queryClient.removeQueries({
    predicate: (query) => query.meta?.sessionScoped === true,
  });

  expect(
    queryClient
      .getQueryCache()
      .getAll()
      .filter((query) => query.queryKey[0] === "playback"),
  ).toHaveLength(0);
});
