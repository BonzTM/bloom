import { z } from "zod";

// Wire shapes mirror api/openapi.yaml (ImportJob, ImportsResponse).
const int64 = () => z.number().int().min(0).max(Number.MAX_SAFE_INTEGER);

export const importSourceSchema = z.enum([
  "playback_reporting",
  "jellystat",
  "bloom_export",
]);

export type ImportSource = z.output<typeof importSourceSchema>;

export const importStateSchema = z.enum([
  "pending",
  "running",
  "completed",
  "failed",
  "cancelled",
]);

export type ImportState = z.output<typeof importStateSchema>;

export const importJobSchema = z.object({
  id: z.uuid(),
  media_server_id: z.uuid(),
  source: importSourceSchema,
  state: importStateSchema,
  read: int64(),
  imported: int64(),
  skipped: int64(),
  duplicate: int64(),
  last_error: z.string().max(512),
  requested_by: z.uuid(),
  created_at: z.iso.datetime({ offset: true }),
  started_at: z.iso.datetime({ offset: true }).optional(),
  finished_at: z.iso.datetime({ offset: true }).optional(),
  updated_at: z.iso.datetime({ offset: true }),
});

export type ImportJob = z.output<typeof importJobSchema>;

export const MAX_IMPORTS_CURSOR_LENGTH = 256;

export const importsCursorSchema = z
  .string()
  .min(1)
  .max(MAX_IMPORTS_CURSOR_LENGTH);

export const importsPageSchema = z.object({
  items: z.array(importJobSchema).max(100),
  next_cursor: z.string().max(MAX_IMPORTS_CURSOR_LENGTH),
});

export type ImportsPage = z.output<typeof importsPageSchema>;

export const importIdSchema = z.uuid();
export const mediaServerIdSchema = z.uuid();

// One active job per server and source; the API answers 409 with this code.
export const IMPORT_IN_PROGRESS = "import_in_progress";

export const MAX_IMPORT_UPLOAD_BYTES = 256 * 1024 * 1024;

export type StartImportInput =
  | Readonly<{ source: "playback_reporting"; mediaServerId: string }>
  | Readonly<{
      source: "bloom_export" | "jellystat";
      mediaServerId: string;
      file: File;
    }>;

// The sources that arrive as an uploaded file.
export function isFileSource(source: ImportSource): boolean {
  return source === "bloom_export" || source === "jellystat";
}
