import { QueryClient } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import type { RenderResult } from "@testing-library/react";
import { StrictMode } from "react";
import type { InitialEntry } from "react-router-dom";
import { AppProviders } from "../app/providers.js";
import { createTestRouter } from "../app/router.js";
import { AuthApi } from "../features/auth/api/auth-api.js";
import { InvitesApi } from "../features/invites/api/invites-api.js";
import { MediaServersApi } from "../features/media-servers/api/media-servers-api.js";
import { NotificationsApi } from "../features/notifications/api/notifications-api.js";
import { PlaybackApi } from "../features/playback/api/playback-api.js";
import { RequestsApi } from "../features/requests/api/requests-api.js";
import { RolesApi } from "../features/roles/api/roles-api.js";
import { SystemApi } from "../features/system/api/system-api.js";
import { ApiClient } from "../lib/api/http-client.js";

export type AppRender = RenderResult &
  Readonly<{
    queryClient: QueryClient;
    router: ReturnType<typeof createTestRouter>;
  }>;

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
  const systemApi = new SystemApi(client);
  const authApi = new AuthApi(client);
  const rolesApi = new RolesApi(client);
  const mediaServersApi = new MediaServersApi(client);
  const invitesApi = new InvitesApi(client);
  const playbackApi = new PlaybackApi(client);
  const requestsApi = new RequestsApi(client);
  const notificationsApi = new NotificationsApi(client);
  const router = createTestRouter([initialEntry]);
  const result = render(
    <StrictMode>
      <AppProviders
        systemApi={systemApi}
        authApi={authApi}
        rolesApi={rolesApi}
        mediaServersApi={mediaServersApi}
        invitesApi={invitesApi}
        playbackApi={playbackApi}
        requestsApi={requestsApi}
        notificationsApi={notificationsApi}
        queryClient={queryClient}
        router={router}
      />
    </StrictMode>,
  );
  return { ...result, queryClient, router };
}
