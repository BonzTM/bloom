import type { ApiClient } from "../../../lib/api/http-client.js";
import {
  ACCOUNT_PAGE,
  accountCursorSchema,
  accountIdSchema,
  accountSearchSchema,
  adminAccountSchema,
  adminAccountsResponseSchema,
  type AdminAccount,
  type AdminAccountsPage,
} from "./accounts-schemas.js";

const ACCOUNTS_PATH = "api/v1/accounts";

// Administrative reads of accounts: a searchable page and one account. The
// server never returns credential material, and the schemas accept none.
export class AccountsApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  list(
    search: string,
    cursor: string | undefined,
    signal: AbortSignal,
  ): Promise<AdminAccountsPage> {
    const query = new URLSearchParams({ limit: String(ACCOUNT_PAGE) });
    const q = accountSearchSchema.parse(search);
    if (q !== "") {
      query.set("q", q);
    }
    if (cursor !== undefined) {
      query.set("cursor", accountCursorSchema.parse(cursor));
    }
    return this.#client.requestJson(
      `${ACCOUNTS_PATH}?${query.toString()}`,
      adminAccountsResponseSchema,
      { signal },
    );
  }

  get(accountId: string, signal: AbortSignal): Promise<AdminAccount> {
    return this.#client.requestJson(
      `${ACCOUNTS_PATH}/${encodeURIComponent(accountIdSchema.parse(accountId))}`,
      adminAccountSchema,
      { signal },
    );
  }
}
