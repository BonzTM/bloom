import { useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { AsyncStatus } from "../../../components/async-status.js";
import { MediaRow } from "../../../components/media-row.js";
import type { MetadataGenre } from "../api/metadata-schemas.js";
import type { MediaKind } from "../api/requests-schemas.js";
import type { useGenres } from "../hooks/requests-queries.js";
import { browsePath } from "./browse-path.js";
import { BRAND_LISTS, type Brand } from "./brands.js";
import { describeSearchError } from "./request-errors.js";

// Genre tiles take one of a few fixed tones in turn, so neighbouring tiles
// never share a colour. TMDB lists genres in a fixed order, so a genre
// keeps its colour from visit to visit.
const GENRE_TONES = 8;

const GENRE_LISTS: Readonly<
  Record<MediaKind, "movies/genres" | "series/genres">
> = {
  movie: "movies/genres",
  series: "series/genres",
};

// A row of genre tiles, each opening that genre's popular titles.
export function GenreRow({
  title,
  kind,
  query,
}: Readonly<{
  title: string;
  kind: MediaKind;
  query: ReturnType<typeof useGenres>;
}>): ReactNode {
  if (query.status === "pending") {
    return (
      <MediaRow
        title={title}
        shape="tile"
        status={<AsyncStatus>Loading {title.toLowerCase()}…</AsyncStatus>}
      />
    );
  }
  if (query.data === undefined) {
    return (
      <MediaRow
        title={title}
        shape="tile"
        status={
          <AsyncStatus kind="alert">
            {describeSearchError(query.error)}
          </AsyncStatus>
        }
      />
    );
  }
  return (
    <MediaRow title={title} shape="tile">
      {query.data.map((genre, index) => (
        <li key={genre.id}>
          <GenreTile genre={genre} kind={kind} tone={index % GENRE_TONES} />
        </li>
      ))}
    </MediaRow>
  );
}

function GenreTile({
  genre,
  kind,
  tone,
}: Readonly<{
  genre: MetadataGenre;
  kind: MediaKind;
  tone: number;
}>): ReactNode {
  return (
    <Link
      to={browsePath({ list: GENRE_LISTS[kind], id: genre.id })}
      className={`media-tile genre-tile genre-tile-tone-${String(tone)}`}
    >
      <span className="media-tile-name">{genre.name}</span>
    </Link>
  );
}

// A row of studio or network logos, each opening its popular titles.
export function BrandRow({
  title,
  brands,
  list,
}: Readonly<{
  title: string;
  brands: readonly Brand[];
  list: keyof typeof BRAND_LISTS;
}>): ReactNode {
  return (
    <MediaRow title={title} shape="tile">
      {brands.map((brand) => (
        <li key={brand.id}>
          <BrandTile brand={brand} list={list} />
        </li>
      ))}
    </MediaRow>
  );
}

// The logo, or the name when the logo cannot be loaded. Either way the
// link is named by the brand.
function BrandTile({
  brand,
  list,
}: Readonly<{ brand: Brand; list: keyof typeof BRAND_LISTS }>): ReactNode {
  const [failed, setFailed] = useState(false);
  return (
    <Link
      to={browsePath({ list: BRAND_LISTS[list], id: brand.id })}
      className="media-tile brand-tile"
      aria-label={brand.name}
    >
      {failed ? (
        <span className="media-tile-name">{brand.name}</span>
      ) : (
        <img
          className="brand-tile-logo"
          src={brand.logo}
          alt=""
          loading="lazy"
          decoding="async"
          onError={() => {
            setFailed(true);
          }}
        />
      )}
    </Link>
  );
}
