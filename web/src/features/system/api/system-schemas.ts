import { z } from "zod/v4";

// Wire contract for `GET /api/v1/version`. Unknown fields are stripped rather
// than rejected so the backend can extend the payload without breaking the UI.
export const versionInfoSchema = z.object({
  name: z.literal("bloom"),
  version: z.string().max(200),
  commit: z.string().max(200),
});

export type VersionInfo = z.output<typeof versionInfoSchema>;
