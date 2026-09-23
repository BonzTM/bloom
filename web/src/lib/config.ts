import { z } from "zod/v4";

const publicConfigSchema = z.object({
  apiBaseUrl: z.url(),
  enableMsw: z.boolean(),
});

export type PublicConfig = z.output<typeof publicConfigSchema>;

// Public build configuration. The API base URL must share the page's origin:
// the session cookie is same-origin by design (ADR 0002), so a cross-origin
// API would silently sign every request out.
export function readPublicConfig(
  environment: Readonly<Record<string, string | boolean | undefined>>,
  origin: string,
): PublicConfig {
  const config = publicConfigSchema.parse({
    apiBaseUrl:
      typeof environment.VITE_API_BASE_URL === "string"
        ? environment.VITE_API_BASE_URL
        : `${origin}/`,
    enableMsw:
      environment.DEV === true && environment.VITE_ENABLE_MSW === "true",
  });
  if (new URL(config.apiBaseUrl).origin !== origin) {
    throw new Error(
      `VITE_API_BASE_URL must share the page origin ${origin}; the session cookie is same-origin only`,
    );
  }
  return config;
}
