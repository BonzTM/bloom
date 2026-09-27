import { z } from "zod";

// Artwork comes through Bloom's own proxy, so the browser never learns the
// media server's address or credential. Mirrors the OpenAPI parameters.
export const itemImageTypeSchema = z.enum(["Primary", "Backdrop", "Thumb"]);

export type ItemImageType = z.output<typeof itemImageTypeSchema>;

export const MIN_ITEM_IMAGE_WIDTH = 64;
export const MAX_ITEM_IMAGE_WIDTH = 1280;

const itemImageWidthSchema = z
  .number()
  .int()
  .min(MIN_ITEM_IMAGE_WIDTH)
  .max(MAX_ITEM_IMAGE_WIDTH);

export function itemImageUrl(
  mediaServerId: string,
  itemId: string,
  type: ItemImageType,
  maxWidth: number,
): string {
  const query = new URLSearchParams({
    type: itemImageTypeSchema.parse(type),
    max_width: String(itemImageWidthSchema.parse(maxWidth)),
  });
  return `/api/v1/media-servers/${encodeURIComponent(mediaServerId)}/items/${encodeURIComponent(itemId)}/image?${query.toString()}`;
}
