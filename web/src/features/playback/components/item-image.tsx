import { useState, type ReactNode } from "react";
import { itemImageUrl, type ItemImageType } from "../api/item-image.js";

export type ItemImageShape = "poster" | "wide";

type ItemImageProps = Readonly<{
  // Without an item there is nothing to fetch: series are ranked by name
  // until the library catalog gives them an item of their own.
  mediaServerId?: string | undefined;
  itemId?: string | undefined;
  title: string;
  shape: ItemImageShape;
  type?: ItemImageType | undefined;
}>;

const WIDTH: Readonly<Record<ItemImageShape, number>> = {
  poster: 400,
  wide: 640,
};

// A media-server item's artwork, or a placeholder carrying the title's
// first letter when there is no item or the image cannot be loaded. The
// surrounding card names the title, so the image itself is decorative.
export function ItemImage({
  mediaServerId,
  itemId,
  title,
  shape,
  type = "Primary",
}: ItemImageProps): ReactNode {
  const [failed, setFailed] = useState(false);
  const className = `item-image item-image-${shape}`;
  if (
    mediaServerId === undefined ||
    itemId === undefined ||
    itemId === "" ||
    failed
  ) {
    return (
      <span
        className={`${className} item-image-placeholder`}
        aria-hidden="true"
      >
        {title.slice(0, 1).toUpperCase()}
      </span>
    );
  }
  return (
    <img
      className={className}
      src={itemImageUrl(mediaServerId, itemId, type, WIDTH[shape])}
      alt=""
      loading="lazy"
      decoding="async"
      onError={() => {
        setFailed(true);
      }}
    />
  );
}
