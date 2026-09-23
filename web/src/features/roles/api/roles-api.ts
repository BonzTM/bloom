import type { ApiClient } from "../../../lib/api/http-client.js";
import {
  requestQuotaInputSchema,
  requestQuotaSchema,
  type RequestQuota,
  type RequestQuotaInput,
} from "./quota-schemas.js";
import {
  roleIdSchema,
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

  // The role's request quota; a 404 means none is set.
  quota(roleId: string, signal: AbortSignal): Promise<RequestQuota> {
    return this.#client.requestJson(quotaPath(roleId), requestQuotaSchema, {
      signal,
    });
  }

  setQuota(roleId: string, input: RequestQuotaInput): Promise<RequestQuota> {
    return this.#client.requestJson(quotaPath(roleId), requestQuotaSchema, {
      method: "PUT",
      body: requestQuotaInputSchema.parse(input),
    });
  }

  removeQuota(roleId: string): Promise<void> {
    return this.#client.requestEmpty(quotaPath(roleId), { method: "DELETE" });
  }
}

function quotaPath(roleId: string): string {
  return `api/v1/roles/${encodeURIComponent(roleIdSchema.parse(roleId))}/request-quota`;
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
