import { QueryClientProvider, type QueryClient } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { RouterProvider, type RouterProviderProps } from "react-router-dom";
import type { SystemApi } from "../features/system/api/system-api.js";
import { SystemApiContext } from "../features/system/system-context.js";

type AppProvidersProps = Readonly<{
  api: SystemApi;
  queryClient: QueryClient;
  router: RouterProviderProps["router"];
}>;

export function AppProviders({
  api,
  queryClient,
  router,
}: AppProvidersProps): ReactNode {
  return (
    <QueryClientProvider client={queryClient}>
      <SystemApiContext value={api}>
        <RouterProvider router={router} />
      </SystemApiContext>
    </QueryClientProvider>
  );
}
