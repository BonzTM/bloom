import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { AsyncStatus } from "../../../components/async-status.js";
import { MediaRow } from "../../../components/media-row.js";
import { ItemImage } from "../../playback/components/item-image.js";
import { CardArt, CardCaption } from "../../requests/components/title-card.js";
import { titleWithYear } from "../../requests/components/request-format.js";
import type { RecentlyAddedTitle } from "../api/catalog-schemas.js";
import type { useRecentlyAdded } from "../hooks/catalog-queries.js";
import { itemPath } from "./catalog-format.js";

const TITLE = "Recently added";
const MAX_SEARCH_LENGTH = 200;

// Where a card leads: the library's own item page for someone who may read
// every statistic, a search for the title for someone who may request, or
// nowhere. The catalog does not keep TMDB identifiers, so a search is the
// way from a library item to its title page.
export type RecentlyAddedLink = "item" | "search" | "none";

export function RecentlyAddedRow({
  query,
  link,
}: Readonly<{
  query: ReturnType<typeof useRecentlyAdded>;
  link: RecentlyAddedLink;
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
          <RecentlyAddedCard item={item} link={link} />
        </li>
      ))}
    </MediaRow>
  );
}

function RecentlyAddedCard({
  item,
  link,
}: Readonly<{ item: RecentlyAddedTitle; link: RecentlyAddedLink }>): ReactNode {
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
  if (link === "none") {
    return <span className="poster-card title-card">{body}</span>;
  }
  return (
    <Link to={cardPath(item, link)} className="poster-card title-card">
      {body}
    </Link>
  );
}

export function cardPath(
  item: RecentlyAddedTitle,
  link: Exclude<RecentlyAddedLink, "none">,
): string {
  if (link === "item") {
    return itemPath(item.media_server_id, item.item_id);
  }
  // A search query is at most 200 characters; a longer name still finds it.
  const query = new URLSearchParams({
    q: item.title.slice(0, MAX_SEARCH_LENGTH),
    kind: item.kind,
  });
  return `/search?${query.toString()}`;
}
