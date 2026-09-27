import type {
  CatalogItem,
  CatalogTypeSummary,
} from "../api/catalog-schemas.js";

const TYPE_PLURALS: Readonly<Record<string, string>> = {
  Movie: "Movies",
  Series: "Series",
  Season: "Seasons",
  Episode: "Episodes",
  MusicAlbum: "Albums",
  Audio: "Tracks",
  MusicArtist: "Artists",
  Book: "Books",
  AudioBook: "Audiobooks",
  Video: "Videos",
  BoxSet: "Collections",
  Playlist: "Playlists",
};

// Jellyfin's item types read as plain words, pluralised for counts.
export function typePlural(itemType: string): string {
  return TYPE_PLURALS[itemType] ?? itemType;
}

// Counts for the types people scan for first; the rest are folded into
// one "Other" figure so a card stays one line per meaningful type.
export function headlineTypes(
  types: readonly CatalogTypeSummary[],
): readonly CatalogTypeSummary[] {
  const shown = types.filter((t) => t.item_type !== "Season" && t.items > 0);
  return [...shown].sort((a, b) => b.items - a.items).slice(0, 4);
}

export function totalPlays(types: readonly CatalogTypeSummary[]): number {
  return types.reduce((sum, t) => sum + t.plays, 0);
}

export function totalWatchSeconds(
  types: readonly CatalogTypeSummary[],
): number {
  return types.reduce((sum, t) => sum + t.watch_seconds, 0);
}

// "Series S02E05 · Name" for an episode, "Name (2021)" otherwise.
export function itemTitle(item: CatalogItem): string {
  if (item.item_type === "Episode" && item.series_name !== "") {
    const numbering =
      item.season_number !== undefined && item.index_number !== undefined
        ? ` S${pad(item.season_number)}E${pad(item.index_number)}`
        : "";
    return `${item.series_name}${numbering} · ${item.name}`;
  }
  return item.production_year === undefined
    ? item.name
    : `${item.name} (${String(item.production_year)})`;
}

export function formatRuntime(runtimeMs: number | undefined): string {
  if (runtimeMs === undefined || runtimeMs <= 0) {
    return "";
  }
  const minutes = Math.round(runtimeMs / 60_000);
  const hours = Math.floor(minutes / 60);
  return hours > 0
    ? `${String(hours)} h ${String(minutes % 60)} min`
    : `${String(minutes)} min`;
}

export function formatDay(value: string | undefined): string {
  return value === undefined ? "" : value.slice(0, 10);
}

function pad(value: number): string {
  return String(value).padStart(2, "0");
}

export function libraryPath(serverId: string, libraryId: string): string {
  return `/admin/libraries/${encodeURIComponent(serverId)}/${encodeURIComponent(libraryId)}`;
}

export function itemPath(serverId: string, itemId: string): string {
  return `/admin/libraries/${encodeURIComponent(serverId)}/items/${encodeURIComponent(itemId)}`;
}
