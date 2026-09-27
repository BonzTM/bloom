import { useId, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { formatCount } from "../../../components/charts/chart-data.js";
import { ItemImage } from "../../playback/components/item-image.js";
import type { CatalogItem } from "../api/catalog-schemas.js";
import { itemPath, itemTitle } from "./catalog-format.js";

type ItemTilesProps = Readonly<{
  title: string;
  items: readonly CatalogItem[];
  empty: string;
}>;

// Artwork tiles for catalog items, each linking to the item page. Episodes
// use the wide frame their thumbnails come in.
export function ItemTiles({ title, items, empty }: ItemTilesProps): ReactNode {
  const id = useId();
  return (
    <figure className="tile-figure" aria-labelledby={id}>
      <figcaption id={id}>{title}</figcaption>
      {items.length === 0 ? (
        <p className="chart-empty">{empty}</p>
      ) : (
        <ul className="tile-grid">
          {items.map((item) => (
            <li key={item.item_id} className="tile">
              <Link
                to={itemPath(item.media_server_id, item.item_id)}
                className="poster-card"
              >
                <ItemImage
                  mediaServerId={item.media_server_id}
                  itemId={item.item_id}
                  title={item.name}
                  shape={item.item_type === "Episode" ? "wide" : "poster"}
                />
                <span className="tile-name">{itemTitle(item)}</span>
                <span className="tile-metric">
                  {formatCount(item.plays)}{" "}
                  {item.plays === 1 ? "play" : "plays"}
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </figure>
  );
}
