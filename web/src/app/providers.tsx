import { QueryClientProvider, type QueryClient } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { RouterProvider, type RouterProviderProps } from "react-router-dom";
import type { AuthApi } from "../features/auth/api/auth-api.js";
import { AuthApiContext } from "../features/auth/auth-context.js";
import type { InvitesApi } from "../features/invites/api/invites-api.js";
import { InvitesApiContext } from "../features/invites/invites-context.js";
import type { MediaServersApi } from "../features/media-servers/api/media-servers-api.js";
import { MediaServersApiContext } from "../features/media-servers/media-servers-context.js";
import type { PlaybackApi } from "../features/playback/api/playback-api.js";
import { PlaybackApiContext } from "../features/playback/playback-context.js";
import type { RolesApi } from "../features/roles/api/roles-api.js";
import { RolesApiContext } from "../features/roles/roles-context.js";
import type { SystemApi } from "../features/system/api/system-api.js";
import { SystemApiContext } from "../features/system/system-context.js";

type AppProvidersProps = Readonly<{
  systemApi: SystemApi;
  authApi: AuthApi;
  rolesApi: RolesApi;
  mediaServersApi: MediaServersApi;
  invitesApi: InvitesApi;
  playbackApi: PlaybackApi;
  queryClient: QueryClient;
  router: RouterProviderProps["router"];
}>;

export function AppProviders({
  systemApi,
  authApi,
  rolesApi,
  mediaServersApi,
  invitesApi,
  playbackApi,
  queryClient,
  router,
}: AppProvidersProps): ReactNode {
  return (
    <QueryClientProvider client={queryClient}>
      <SystemApiContext value={systemApi}>
        <AuthApiContext value={authApi}>
          <RolesApiContext value={rolesApi}>
            <MediaServersApiContext value={mediaServersApi}>
              <InvitesApiContext value={invitesApi}>
                <PlaybackApiContext value={playbackApi}>
                  <RouterProvider router={router} />
                </PlaybackApiContext>
              </InvitesApiContext>
            </MediaServersApiContext>
          </RolesApiContext>
        </AuthApiContext>
      </SystemApiContext>
    </QueryClientProvider>
  );
}
