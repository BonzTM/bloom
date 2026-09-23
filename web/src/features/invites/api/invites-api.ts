import type { ApiClient } from "../../../lib/api/http-client.js";
import {
  acceptedInviteSchema,
  acceptInviteRequestSchema,
  createdInviteSchema,
  createInviteRequestSchema,
  inviteCodeSchema,
  inviteIdSchema,
  invitesCursorSchema,
  invitesPageSchema,
  publicInviteSchema,
  type AcceptedInvite,
  type AcceptInviteRequest,
  type CreatedInvite,
  type CreateInviteRequest,
  type InvitesPage,
  type PublicInvite,
} from "./invites-schemas.js";

const ADMIN_PATH = "api/v1/invites";
const PUBLIC_PATH = "api/v1/invite";

export class InvitesApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  // One page of invites, newest first. `cursor` is the previous page's
  // `next_cursor`; omit it for the first page.
  list(cursor: string | undefined, signal: AbortSignal): Promise<InvitesPage> {
    return this.#client.requestJson(listPath(cursor), invitesPageSchema, {
      signal,
    });
  }

  // Creates an invite and returns its code exactly once.
  create(input: CreateInviteRequest): Promise<CreatedInvite> {
    return this.#client.requestJson(ADMIN_PATH, createdInviteSchema, {
      method: "POST",
      body: createInviteRequestSchema.parse(input),
    });
  }

  revoke(id: string): Promise<void> {
    return this.#client.requestEmpty(
      `${ADMIN_PATH}/${encodeURIComponent(inviteIdSchema.parse(id))}`,
      { method: "DELETE" },
    );
  }

  // What a visitor may learn about an invite before accepting it: the
  // server's name and the rules for the account they will create.
  preview(code: string, signal: AbortSignal): Promise<PublicInvite> {
    return this.#client.requestJson(publicPath(code), publicInviteSchema, {
      signal,
    });
  }

  // The password travels once, in this request body, straight to the media
  // server through Bloom; it is never stored or returned.
  accept(code: string, input: AcceptInviteRequest): Promise<AcceptedInvite> {
    return this.#client.requestJson(
      `${publicPath(code)}/accept`,
      acceptedInviteSchema,
      { method: "POST", body: acceptInviteRequestSchema.parse(input) },
    );
  }
}

function listPath(cursor: string | undefined): string {
  if (cursor === undefined) {
    return ADMIN_PATH;
  }
  const query = new URLSearchParams({
    cursor: invitesCursorSchema.parse(cursor),
  });
  return `${ADMIN_PATH}?${query.toString()}`;
}

function publicPath(code: string): string {
  return `${PUBLIC_PATH}/${inviteCodeSchema.parse(code)}`;
}
