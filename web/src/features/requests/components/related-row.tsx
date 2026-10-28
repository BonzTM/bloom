import type { ReactNode } from "react";
import type { MetadataDiscoverTitle } from "../api/metadata-schemas.js";
import { PosterRow } from "./poster-row.js";

type RelatedRowProps = Readonly<{
  title: string;
  items: readonly MetadataDiscoverTitle[];
}>;

// A title page's row of recommended or similar titles, drawn like the home
// page's rows. TMDB sends one page with the title, so the row never grows;
// an empty row is left out.
export function RelatedRow({ title, items }: RelatedRowProps): ReactNode {
  if (items.length === 0) {
    return null;
  }
  return <PosterRow title={title} titles={items} />;
}
