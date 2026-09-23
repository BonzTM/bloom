import type { ApiClient } from "../../../lib/api/http-client.js";
import {
  rolesCursorSchema,
  rolesPageSchema,
  type RolesPage,
} from "./roles-schemas.js";

export class RolesApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  // One page of roles. `cursor` is the previous page's `next_cursor`; omit it
  // for the first page.
  list(cursor: string | undefined, signal: AbortSignal): Promise<RolesPage> {
    return this.#client.requestJson(rolesPath(cursor), rolesPageSchema, {
      signal,
    });
  }
}

function rolesPath(cursor: string | undefined): string {
  if (cursor === undefined) {
    return "api/v1/roles";
  }
  const query = new URLSearchParams({
    cursor: rolesCursorSchema.parse(cursor),
  });
  return `api/v1/roles?${query.toString()}`;
}
