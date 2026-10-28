import { useId, type ReactNode } from "react";
import type { MetadataDiscoverTitle } from "../api/metadata-schemas.js";
import { DiscoverCard } from "./discover-row.js";

type RelatedRowProps = Readonly<{
  title: string;
  items: readonly MetadataDiscoverTitle[];
}>;

// A title page's row of recommended or similar titles. TMDB sends one
// page with the title, so the row never grows; an empty row is left out.
export function RelatedRow({ title, items }: RelatedRowProps): ReactNode {
  const id = useId();
  if (items.length === 0) {
    return null;
  }
  return (
    <section aria-labelledby={id} className="discover-row">
      <h2 id={id}>{title}</h2>
      <div className="discover-scroller">
        <ul className="discover-list" aria-label={title}>
          {items.map((item) => (
            <li key={`${item.kind}-${item.provider_id}`}>
              <DiscoverCard item={item} />
            </li>
          ))}
        </ul>
      </div>
    </section>
  );
}
