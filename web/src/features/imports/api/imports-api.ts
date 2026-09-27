import type { ApiClient } from "../../../lib/api/http-client.js";
import {
  importIdSchema,
  importJobSchema,
  importsCursorSchema,
  importsPageSchema,
  mediaServerIdSchema,
  type ImportJob,
  type ImportsPage,
  type StartImportInput,
} from "./imports-schemas.js";

const BASE_PATH = "api/v1/imports";
const EXPORT_PATH = "api/v1/exports/watches";
const PAGE_SIZE = 50;

export class ImportsApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  // One page of jobs, newest first. `cursor` is the previous page's
  // `next_cursor`; omit it for the first page.
  list(cursor: string | undefined, signal: AbortSignal): Promise<ImportsPage> {
    return this.#client.requestJson(listPath(cursor), importsPageSchema, {
      signal,
    });
  }

  get(id: string, signal: AbortSignal): Promise<ImportJob> {
    return this.#client.requestJson(jobPath(id), importJobSchema, { signal });
  }

  // A Playback Reporting job is a JSON body; a Bloom export job carries the
  // file as multipart, which the browser encodes itself.
  start(input: StartImportInput): Promise<ImportJob> {
    const mediaServerId = mediaServerIdSchema.parse(input.mediaServerId);
    if (input.source === "playback_reporting") {
      return this.#client.requestJson(BASE_PATH, importJobSchema, {
        method: "POST",
        body: { media_server_id: mediaServerId, source: input.source },
      });
    }
    const form = new FormData();
    form.set("media_server_id", mediaServerId);
    form.set("source", input.source);
    // A Bloom export is a zip; an older export is JSON Lines. The server
    // detects the shape by content, so the file goes with the type the
    // browser gave it, re-wrapped so the form owns a File of its own realm.
    form.set(
      "file",
      new File([input.file], input.file.name, {
        type:
          input.file.type === "" ? "application/octet-stream" : input.file.type,
      }),
    );
    return this.#client.requestJson(BASE_PATH, importJobSchema, {
      method: "POST",
      body: form,
    });
  }

  cancel(id: string): Promise<ImportJob> {
    return this.#client.requestJson(`${jobPath(id)}/cancel`, importJobSchema, {
      method: "POST",
    });
  }
}

// The export is one zip the browser downloads with the session cookie:
// everything Bloom has, streamed straight from the database.
export function exportWatchesPath(mediaServerId: string | undefined): string {
  if (mediaServerId === undefined) {
    return `/${EXPORT_PATH}`;
  }
  const query = new URLSearchParams({
    media_server_id: mediaServerIdSchema.parse(mediaServerId),
  });
  return `/${EXPORT_PATH}?${query.toString()}`;
}

function listPath(cursor: string | undefined): string {
  const query = new URLSearchParams({ page_size: String(PAGE_SIZE) });
  if (cursor !== undefined) {
    query.set("cursor", importsCursorSchema.parse(cursor));
  }
  return `${BASE_PATH}?${query.toString()}`;
}

function jobPath(id: string): string {
  return `${BASE_PATH}/${encodeURIComponent(importIdSchema.parse(id))}`;
}
