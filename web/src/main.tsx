import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { ApiClient } from "./lib/api/http-client.js";
import { readPublicConfig } from "./lib/config.js";
import { AuthApi } from "./features/auth/api/auth-api.js";
import { RolesApi } from "./features/roles/api/roles-api.js";
import { SystemApi } from "./features/system/api/system-api.js";
import { AppProviders } from "./app/providers.js";
import { createQueryClient } from "./app/query-client.js";
import { createAppRouter } from "./app/router.js";
import "./styles.css";

async function start(): Promise<void> {
  const config = readPublicConfig(import.meta.env, window.location.origin);
  if (import.meta.env.DEV && config.enableMsw) {
    const { worker } = await import("./mocks/browser.js");
    await worker.start({ onUnhandledRequest: "bypass" });
  }
  const rootElement = document.querySelector<HTMLElement>("#root");
  if (rootElement === null) {
    throw new Error("Application root element is missing");
  }
  const client = new ApiClient(new URL(config.apiBaseUrl));
  const systemApi = new SystemApi(client);
  const authApi = new AuthApi(client);
  const rolesApi = new RolesApi(client);
  createRoot(rootElement).render(
    <StrictMode>
      <AppProviders
        systemApi={systemApi}
        authApi={authApi}
        rolesApi={rolesApi}
        queryClient={createQueryClient()}
        router={createAppRouter()}
      />
    </StrictMode>,
  );
}

void start().catch((error: unknown) => {
  console.error("Application startup failed", error);
});
