import {
  PROBE_TIMEOUT_MS,
  type ApiClient,
} from "../../../lib/api/http-client.js";
import {
  mediaServerIdSchema,
  mediaServersCursorSchema,
  mediaServerExclusionsSchema,
  mediaServersPageSchema,
  registeredMediaServerSchema,
  registerMediaServerRequestSchema,
  replaceExclusionsRequestSchema,
  type MediaServerExclusions,
  type MediaServersPage,
  type RegisteredMediaServer,
  type RegisterMediaServerInput,
  type ReplaceExclusionsInput,
} from "./media-servers-schemas.js";

const BASE_PATH = "api/v1/media-servers";

export class MediaServersApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  // One page of registered servers. `cursor` is the previous page's
  // `next_cursor`; omit it for the first page.
  list(
    cursor: string | undefined,
    signal: AbortSignal,
  ): Promise<MediaServersPage> {
    return this.#client.requestJson(listPath(cursor), mediaServersPageSchema, {
      signal,
    });
  }

  // Probes the server with the API key and stores it only when the probe
  // succeeds. The API key travels once, in this request body, and never
  // comes back.
  register(input: RegisterMediaServerInput): Promise<RegisteredMediaServer> {
    return this.#client.requestJson(BASE_PATH, registeredMediaServerSchema, {
      method: "POST",
      body: registerMediaServerRequestSchema.parse(input),
      timeoutMs: PROBE_TIMEOUT_MS,
    });
  }

  remove(id: string): Promise<void> {
    return this.#client.requestEmpty(serverPath(id), { method: "DELETE" });
  }

  exclusions(id: string, signal: AbortSignal): Promise<MediaServerExclusions> {
    return this.#client.requestJson(
      `${serverPath(id)}/exclusions`,
      mediaServerExclusionsSchema,
      { signal },
    );
  }

  // Replaces both lists at once; the server answers with what it stored.
  replaceExclusions(
    id: string,
    input: ReplaceExclusionsInput,
  ): Promise<MediaServerExclusions> {
    return this.#client.requestJson(
      `${serverPath(id)}/exclusions`,
      mediaServerExclusionsSchema,
      { method: "PUT", body: replaceExclusionsRequestSchema.parse(input) },
    );
  }
}

function listPath(cursor: string | undefined): string {
  if (cursor === undefined) {
    return BASE_PATH;
  }
  const query = new URLSearchParams({
    cursor: mediaServersCursorSchema.parse(cursor),
  });
  return `${BASE_PATH}?${query.toString()}`;
}

function serverPath(id: string): string {
  return `${BASE_PATH}/${encodeURIComponent(mediaServerIdSchema.parse(id))}`;
}
