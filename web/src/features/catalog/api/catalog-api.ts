import type { ApiClient } from "../../../lib/api/http-client.js";
import {
  CATALOG_PAGE,
  catalogDaysSchema,
  catalogGenresResponseSchema,
  catalogHistoryResponseSchema,
  catalogItemDetailSchema,
  catalogItemsResponseSchema,
  catalogLibrariesResponseSchema,
  catalogOrderSchema,
  catalogSortSchema,
  catalogSyncResponseSchema,
  itemIdSchema,
  librariesResponseSchema,
  libraryIdSchema,
  mediaServerIdSchema,
  type CatalogGenreSummary,
  type CatalogHistoryPage,
  type CatalogItem,
  type CatalogItemDetail,
  type CatalogItemsPage,
  type CatalogItemsParams,
  type CatalogLibrarySummary,
  type Library,
} from "./catalog-schemas.js";

const SERVERS_PATH = "api/v1/media-servers";

export class CatalogApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  // The server's libraries by name; the catalog summaries key by id only.
  libraries(
    serverId: string,
    signal: AbortSignal,
  ): Promise<readonly Library[]> {
    return this.#client
      .requestJson(
        `${serverPath(serverId)}/libraries`,
        librariesResponseSchema,
        {
          signal,
        },
      )
      .then((response) => response.items);
  }

  summary(
    serverId: string,
    days: number | undefined,
    signal: AbortSignal,
  ): Promise<readonly CatalogLibrarySummary[]> {
    return this.#client
      .requestJson(
        `${serverPath(serverId)}/libraries/catalog${daysQuery(days)}`,
        catalogLibrariesResponseSchema,
        { signal },
      )
      .then((response) => response.items);
  }

  items(
    serverId: string,
    libraryId: string,
    params: CatalogItemsParams,
    offset: number,
    signal: AbortSignal,
  ): Promise<CatalogItemsPage> {
    const query = new URLSearchParams({
      limit: String(CATALOG_PAGE),
      offset: String(offset),
      sort: catalogSortSchema.parse(params.sort),
      order: catalogOrderSchema.parse(params.order),
    });
    if (params.days !== undefined) {
      query.set("days", String(catalogDaysSchema.parse(params.days)));
    }
    if (params.itemType !== undefined && params.itemType !== "") {
      query.set("item_type", params.itemType);
    }
    if (params.archived === true) {
      query.set("archived", "true");
    }
    return this.#client.requestJson(
      `${libraryPath(serverId, libraryId)}/items?${query.toString()}`,
      catalogItemsResponseSchema,
      { signal },
    );
  }

  item(
    serverId: string,
    itemId: string,
    signal: AbortSignal,
  ): Promise<CatalogItemDetail> {
    return this.#client.requestJson(
      itemPath(serverId, itemId),
      catalogItemDetailSchema,
      { signal },
    );
  }

  history(
    serverId: string,
    itemId: string,
    offset: number,
    signal: AbortSignal,
  ): Promise<CatalogHistoryPage> {
    const query = new URLSearchParams({
      limit: String(CATALOG_PAGE),
      offset: String(offset),
    });
    return this.#client.requestJson(
      `${itemPath(serverId, itemId)}/history?${query.toString()}`,
      catalogHistoryResponseSchema,
      { signal },
    );
  }

  recent(
    serverId: string,
    libraryId: string,
    signal: AbortSignal,
  ): Promise<readonly CatalogItem[]> {
    return this.#client
      .requestJson(
        `${libraryPath(serverId, libraryId)}/recent?limit=20`,
        catalogItemsResponseSchema,
        { signal },
      )
      .then((response) => response.items);
  }

  genres(
    serverId: string,
    libraryId: string,
    days: number | undefined,
    signal: AbortSignal,
  ): Promise<readonly CatalogGenreSummary[]> {
    return this.#client
      .requestJson(
        `${libraryPath(serverId, libraryId)}/genres${daysQuery(days)}`,
        catalogGenresResponseSchema,
        { signal },
      )
      .then((response) => response.items);
  }

  stale(
    serverId: string,
    libraryId: string,
    days: number,
    offset: number,
    signal: AbortSignal,
  ): Promise<CatalogItemsPage> {
    const query = new URLSearchParams({
      days: String(catalogDaysSchema.parse(days)),
      limit: String(CATALOG_PAGE),
      offset: String(offset),
    });
    return this.#client.requestJson(
      `${libraryPath(serverId, libraryId)}/stale?${query.toString()}`,
      catalogItemsResponseSchema,
      { signal },
    );
  }

  // Asks for a full walk now; 409 means one is already running.
  sync(serverId: string): Promise<void> {
    return this.#client
      .requestJson(
        `${serverPath(serverId)}/catalog/sync`,
        catalogSyncResponseSchema,
        {
          method: "POST",
        },
      )
      .then(() => undefined);
  }
}

function daysQuery(days: number | undefined): string {
  return days === undefined
    ? ""
    : `?days=${String(catalogDaysSchema.parse(days))}`;
}

function serverPath(serverId: string): string {
  return `${SERVERS_PATH}/${encodeURIComponent(mediaServerIdSchema.parse(serverId))}`;
}

function libraryPath(serverId: string, libraryId: string): string {
  return `${serverPath(serverId)}/libraries/${encodeURIComponent(libraryIdSchema.parse(libraryId))}`;
}

function itemPath(serverId: string, itemId: string): string {
  return `${serverPath(serverId)}/items/${encodeURIComponent(itemIdSchema.parse(itemId))}`;
}
