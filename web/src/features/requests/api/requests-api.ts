import type { ApiClient } from "../../../lib/api/http-client.js";
import {
  createMediaRequestSchema,
  metadataSearchResponseSchema,
  metadataSeriesSchema,
  metadataTitleSchema,
  providerIdSchema,
  searchQuerySchema,
  type CreateMediaRequest,
  type MetadataSeries,
  type MetadataTitle,
} from "./metadata-schemas.js";
import {
  mediaKindSchema,
  mediaRequestSchema,
  mediaRequestsPageSchema,
  metadataKeyPresenceSchema,
  metadataKeyRequestSchema,
  profilesCursorSchema,
  requestDecisionSchema,
  requestIdSchema,
  requestProfileInputSchema,
  requestProfileSchema,
  requestProfilesPageSchema,
  requestsCursorSchema,
  requestStatusSchema,
  type MediaKind,
  type MediaRequest,
  type MediaRequestsPage,
  type MetadataKeyPresence,
  type MetadataKeyRequest,
  type RequestDecision,
  type RequestProfile,
  type RequestProfileInput,
  type RequestProfilesPage,
  type RequestStatus,
} from "./requests-schemas.js";

const METADATA_PATH = "api/v1/metadata";
const PROFILES_PATH = "api/v1/request-profiles";
const REQUESTS_PATH = "api/v1/requests";
const KEY_PATH = "api/v1/metadata/providers/tmdb/key";

export type RequestsFilter = Readonly<{
  status?: RequestStatus;
  requesterId?: string;
}>;

export class RequestsApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  // ---- request profiles (admin.settings)

  listProfiles(
    cursor: string | undefined,
    signal: AbortSignal,
  ): Promise<RequestProfilesPage> {
    return this.#client.requestJson(
      pagedPath(PROFILES_PATH, {}, cursor, profilesCursorSchema),
      requestProfilesPageSchema,
      { signal },
    );
  }

  createProfile(input: RequestProfileInput): Promise<RequestProfile> {
    return this.#client.requestJson(PROFILES_PATH, requestProfileSchema, {
      method: "POST",
      body: requestProfileInputSchema.parse(input),
    });
  }

  updateProfile(
    id: string,
    input: RequestProfileInput,
  ): Promise<RequestProfile> {
    return this.#client.requestJson(
      `${PROFILES_PATH}/${uuidSegment(id)}`,
      requestProfileSchema,
      { method: "PUT", body: requestProfileInputSchema.parse(input) },
    );
  }

  removeProfile(id: string): Promise<void> {
    return this.#client.requestEmpty(`${PROFILES_PATH}/${uuidSegment(id)}`, {
      method: "DELETE",
    });
  }

  // ---- metadata (requests.create)

  search(
    query: string,
    kind: MediaKind | undefined,
    signal: AbortSignal,
  ): Promise<readonly MetadataTitle[]> {
    const params = new URLSearchParams({ q: searchQuerySchema.parse(query) });
    if (kind !== undefined) {
      params.set("kind", mediaKindSchema.parse(kind));
    }
    return this.#client
      .requestJson(
        `${METADATA_PATH}/search?${params.toString()}`,
        metadataSearchResponseSchema,
        { signal },
      )
      .then((response) => response.items);
  }

  movie(providerId: string, signal: AbortSignal): Promise<MetadataTitle> {
    return this.#client.requestJson(
      `${METADATA_PATH}/movies/${providerIdSchema.parse(providerId)}`,
      metadataTitleSchema,
      { signal },
    );
  }

  series(providerId: string, signal: AbortSignal): Promise<MetadataSeries> {
    return this.#client.requestJson(
      `${METADATA_PATH}/series/${providerIdSchema.parse(providerId)}`,
      metadataSeriesSchema,
      { signal },
    );
  }

  // ---- requests

  create(input: CreateMediaRequest): Promise<MediaRequest> {
    return this.#client.requestJson(REQUESTS_PATH, mediaRequestSchema, {
      method: "POST",
      body: createMediaRequestSchema.parse(input),
    });
  }

  listRequests(
    filter: RequestsFilter,
    cursor: string | undefined,
    signal: AbortSignal,
  ): Promise<MediaRequestsPage> {
    const query: Record<string, string> = {};
    if (filter.status !== undefined) {
      query.status = requestStatusSchema.parse(filter.status);
    }
    if (filter.requesterId !== undefined) {
      query.requester_id = requestIdSchema.parse(filter.requesterId);
    }
    return this.#client.requestJson(
      pagedPath(REQUESTS_PATH, query, cursor, requestsCursorSchema),
      mediaRequestsPageSchema,
      { signal },
    );
  }

  approve(id: string, decision: RequestDecision): Promise<MediaRequest> {
    return this.#decide(id, "approve", decision);
  }

  decline(id: string, decision: RequestDecision): Promise<MediaRequest> {
    return this.#decide(id, "decline", decision);
  }

  #decide(
    id: string,
    verb: "approve" | "decline",
    decision: RequestDecision,
  ): Promise<MediaRequest> {
    return this.#client.requestJson(
      `${REQUESTS_PATH}/${uuidSegment(id)}/${verb}`,
      mediaRequestSchema,
      { method: "POST", body: requestDecisionSchema.parse(decision) },
    );
  }

  // ---- TMDB key (admin.settings); the key travels once and never returns

  keyPresence(signal: AbortSignal): Promise<MetadataKeyPresence> {
    return this.#client.requestJson(KEY_PATH, metadataKeyPresenceSchema, {
      signal,
    });
  }

  setKey(input: MetadataKeyRequest): Promise<MetadataKeyPresence> {
    return this.#client.requestJson(KEY_PATH, metadataKeyPresenceSchema, {
      method: "PUT",
      body: metadataKeyRequestSchema.parse(input),
    });
  }

  removeKey(): Promise<void> {
    return this.#client.requestEmpty(KEY_PATH, { method: "DELETE" });
  }
}

function pagedPath(
  base: string,
  query: Readonly<Record<string, string>>,
  cursor: string | undefined,
  cursorSchema: { parse: (value: string) => string },
): string {
  const params = new URLSearchParams(query);
  if (cursor !== undefined) {
    params.set("cursor", cursorSchema.parse(cursor));
  }
  const encoded = params.toString();
  return encoded === "" ? base : `${base}?${encoded}`;
}

function uuidSegment(id: string): string {
  return encodeURIComponent(requestIdSchema.parse(id));
}
