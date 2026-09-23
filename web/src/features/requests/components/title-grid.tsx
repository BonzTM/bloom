import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import type { MetadataTitle } from "../api/metadata-schemas.js";
import type { MediaKind } from "../api/requests-schemas.js";
import { Poster } from "./poster.js";
import { kindLabel, titleWithYear } from "./request-format.js";

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
  titles: readonly MetadataTitle[];
}>;

// Posters in a responsive grid, each a link to the title's page.
export function TitleGrid({ label, titles }: TitleGridProps): ReactNode {
  return (
    <ul className="poster-grid" aria-label={label}>
      {titles.map((title) => (
        <li key={`${title.kind}-${title.provider_id}`}>
          <Link to={titlePath(title)} className="poster-card">
            <Poster
              posterPath={title.poster_path}
              title={title.title}
              size="w342"
            />
            <span className="poster-card-title">{titleWithYear(title)}</span>
            <span className="poster-card-kind">{kindLabel(title.kind)}</span>
          </Link>
        </li>
      ))}
    </ul>
  );
}
