import type { ReactNode } from "react";
import type { MetadataTitle } from "../api/metadata-schemas.js";
import type { MediaKind } from "../api/requests-schemas.js";
import type { RowTitle } from "./poster-row.js";
import { TitleCard } from "./title-card.js";

const KIND_SEGMENTS: Readonly<Record<MediaKind, string>> = {
  movie: "movies",
  series: "series",
};

export function titlePath(
  title: Pick<MetadataTitle, "kind" | "provider_id">,
): string {
  return `/requests/${KIND_SEGMENTS[title.kind]}/${title.provider_id}`;
}

type TitleGridProps = Readonly<{
  label: string;
  titles: readonly RowTitle[];
  children?: ReactNode;
}>;

// Posters in a responsive grid, each a link to the title's page, with any
// trailing cell such as "More".
export function TitleGrid({
  label,
  titles,
  children,
}: TitleGridProps): ReactNode {
  return (
    <ul className="poster-grid" aria-label={label}>
      {titles.map((title) => (
        <li key={`${title.kind}-${title.provider_id}`}>
          <TitleCard title={title} state={title.request_state} />
        </li>
      ))}
      {children}
    </ul>
  );
}
