import { z } from "zod/v4";

const publicConfigSchema = z.object({
  apiBaseUrl: z.url(),
  enableMsw: z.boolean(),
});

export type PublicConfig = z.output<typeof publicConfigSchema>;

export function readPublicConfig(
  environment: Readonly<Record<string, string | boolean | undefined>>,
  origin: string,
): PublicConfig {
  return publicConfigSchema.parse({
    apiBaseUrl:
      typeof environment.VITE_API_BASE_URL === "string"
        ? environment.VITE_API_BASE_URL
        : `${origin}/`,
    enableMsw: environment.DEV && environment.VITE_ENABLE_MSW === "true",
  });
}
