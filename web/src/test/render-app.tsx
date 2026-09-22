import { QueryClient } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import type { RenderResult } from "@testing-library/react";
import { StrictMode } from "react";
import type { InitialEntry } from "react-router-dom";
import { AppProviders } from "../app/providers.js";
import { createTestRouter } from "../app/router.js";
import { AuthApi } from "../features/auth/api/auth-api.js";
import { SystemApi } from "../features/system/api/system-api.js";
import { ApiClient } from "../lib/api/http-client.js";

type AppRender = RenderResult & Readonly<{ queryClient: QueryClient }>;

// Renders the whole application under Strict Mode, as production does, so
// double-invoked effects and renders are exercised by every route test. The
// query client is returned so a test can drive cache events directly.
export function renderApp(initialEntry: InitialEntry = "/"): AppRender {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: Number.POSITIVE_INFINITY },
      mutations: { retry: false },
    },
  });
  const client = new ApiClient(new URL("http://localhost/"));
  const api = new SystemApi(client);
  const authApi = new AuthApi(client);
  const result = render(
    <StrictMode>
      <AppProviders
        api={api}
        authApi={authApi}
        queryClient={queryClient}
        router={createTestRouter([initialEntry])}
      />
    </StrictMode>,
  );
  return { ...result, queryClient };
}
