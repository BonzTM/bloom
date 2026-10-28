import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { AsyncStatus } from "../../../components/async-status.js";
import { MediaRow } from "../../../components/media-row.js";
import { ItemImage } from "../../playback/components/item-image.js";
import { CardArt, CardCaption } from "../../requests/components/title-card.js";
import { titleWithYear } from "../../requests/components/request-format.js";
import { titlePath } from "../../requests/components/title-grid.js";
import type { RecentlyAddedTitle } from "../api/catalog-schemas.js";
import type { useRecentlyAdded } from "../hooks/catalog-queries.js";
import { itemPath } from "./catalog-format.js";

const TITLE = "Recently added";
const MAX_SEARCH_LENGTH = 200;

// What a card may open for this account: title pages need requests.create,
// and the library's item pages need stats.read.all.
export type RecentlyAddedAccess = Readonly<{ titles: boolean; items: boolean }>;

export function RecentlyAddedRow({
  query,
  access,
}: Readonly<{
  query: ReturnType<typeof useRecentlyAdded>;
  access: RecentlyAddedAccess;
}>): ReactNode {
  if (query.status === "pending") {
    return (
      <MediaRow
        title={TITLE}
        status={<AsyncStatus>Loading recently added titles…</AsyncStatus>}
      />
    );
  }
  if (query.data === undefined) {
    return (
      <MediaRow
        title={TITLE}
        status={
          <AsyncStatus kind="alert">
            Recently added titles could not be loaded.
          </AsyncStatus>
        }
      />
    );
  }
  if (query.data.length === 0) {
    return (
      <MediaRow
        title={TITLE}
        status={
          <p className="chart-empty">
            Nothing has been added yet. Titles appear once the library catalog
            has synced.
          </p>
        }
      />
    );
  }
  return (
    <MediaRow title={TITLE}>
      {query.data.map((item) => (
        <li key={`${item.media_server_id}-${item.item_id}`}>
          <RecentlyAddedCard item={item} access={access} />
        </li>
      ))}
    </MediaRow>
  );
}

function RecentlyAddedCard({
  item,
  access,
}: Readonly<{
  item: RecentlyAddedTitle;
  access: RecentlyAddedAccess;
}>): ReactNode {
  const body = (
    <>
      <CardArt kind={item.kind} mark="available">
        <ItemImage
          mediaServerId={item.media_server_id}
          itemId={item.item_id}
          title={item.title}
          shape="poster"
        />
      </CardArt>
      <CardCaption
        title={titleWithYear(item)}
        kind={item.kind}
        mark="available"
      />
    </>
  );
  const path = cardPath(item, access);
  if (path === undefined) {
    return <span className="poster-card title-card">{body}</span>;
  }
  return (
    <Link to={path} className="poster-card title-card">
      {body}
    </Link>
  );
}

// Where a card leads: the title page when the media server knows the
// title's TMDB ID, else the library's item page, else a search for the
// title, or nowhere this account may go.
export function cardPath(
  item: RecentlyAddedTitle,
  access: RecentlyAddedAccess,
): string | undefined {
  if (access.titles && item.tmdb_id > 0) {
    return titlePath({ kind: item.kind, provider_id: String(item.tmdb_id) });
  }
  if (access.items) {
    return itemPath(item.media_server_id, item.item_id);
  }
  if (!access.titles) {
    return undefined;
  }
  // A search query is at most 200 characters; a longer name still finds it.
  const query = new URLSearchParams({
    q: item.title.slice(0, MAX_SEARCH_LENGTH),
    kind: item.kind,
  });
  return `/search?${query.toString()}`;
}
