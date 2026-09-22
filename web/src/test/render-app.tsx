import { QueryClient } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import type { RenderResult } from "@testing-library/react";
import { AppProviders } from "../app/providers.js";
import { createTestRouter } from "../app/router.js";
import { SystemApi } from "../features/system/api/system-api.js";
import { ApiClient } from "../lib/api/http-client.js";

export function renderApp(initialEntry = "/"): RenderResult {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: Number.POSITIVE_INFINITY },
      mutations: { retry: false },
    },
  });
  const api = new SystemApi(new ApiClient(new URL("http://localhost/")));
  return render(
    <AppProviders
      api={api}
      queryClient={queryClient}
      router={createTestRouter([initialEntry])}
    />,
  );
}
