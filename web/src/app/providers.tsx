import { QueryClientProvider, type QueryClient } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { RouterProvider, type RouterProviderProps } from "react-router-dom";
import type { AuthApi } from "../features/auth/api/auth-api.js";
import { AuthApiContext } from "../features/auth/auth-context.js";
import type { RolesApi } from "../features/roles/api/roles-api.js";
import { RolesApiContext } from "../features/roles/roles-context.js";
import type { SystemApi } from "../features/system/api/system-api.js";
import { SystemApiContext } from "../features/system/system-context.js";

type AppProvidersProps = Readonly<{
  systemApi: SystemApi;
  authApi: AuthApi;
  rolesApi: RolesApi;
  queryClient: QueryClient;
  router: RouterProviderProps["router"];
}>;

export function AppProviders({
  systemApi,
  authApi,
  rolesApi,
  queryClient,
  router,
}: AppProvidersProps): ReactNode {
  return (
    <QueryClientProvider client={queryClient}>
      <SystemApiContext value={systemApi}>
        <AuthApiContext value={authApi}>
          <RolesApiContext value={rolesApi}>
            <RouterProvider router={router} />
          </RolesApiContext>
        </AuthApiContext>
      </SystemApiContext>
    </QueryClientProvider>
  );
}
