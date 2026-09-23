import { useState, type ReactNode } from "react";

// TMDB serves posters from its image host; the SPA's content security
// policy allows exactly that origin for images.
const POSTER_ORIGIN = "https://image.tmdb.org/t/p";

export type PosterSize = "w342" | "w500";

export function posterUrl(posterPath: string, size: PosterSize): string {
  return `${POSTER_ORIGIN}/${size}${posterPath}`;
}

type PosterProps = Readonly<{
  posterPath: string;
  title: string;
  size: PosterSize;
}>;

// A poster, or a placeholder carrying the title's first letter when the
// provider has none or the image cannot be loaded. The title is announced
// by the surrounding link or heading, so the image itself is decorative.
export function Poster({ posterPath, title, size }: PosterProps): ReactNode {
  const [failed, setFailed] = useState(false);
  if (posterPath === "" || failed) {
    return (
      <span className="poster poster-placeholder" aria-hidden="true">
        {title.slice(0, 1)}
      </span>
    );
  }
  return (
    <img
      className="poster"
      src={posterUrl(posterPath, size)}
      alt=""
      loading="lazy"
      decoding="async"
      onError={() => {
        setFailed(true);
      }}
    />
  );
}
