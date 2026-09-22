import type { ApiClient } from "../../../lib/api/http-client.js";
import { versionInfoSchema, type VersionInfo } from "./system-schemas.js";

export class SystemApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  version(signal: AbortSignal): Promise<VersionInfo> {
    return this.#client.requestJson("api/v1/version", versionInfoSchema, {
      signal,
    });
  }
}
