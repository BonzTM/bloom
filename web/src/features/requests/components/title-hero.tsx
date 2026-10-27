import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import type {
  MetadataDetails,
  MetadataTitle,
} from "../api/metadata-schemas.js";
import { formatMinutes, youTubeUrl } from "./detail-format.js";
import { backdropUrl, Poster } from "./poster.js";

type TitleHeroProps = Readonly<{
  title: MetadataTitle;
  details: MetadataDetails;
  // The title's actions: following it, its trailer, requesting it.
  actions: ReactNode;
}>;

// The full-bleed header of a title page: the backdrop behind the poster,
// the title and year, its rating, runtime, and genres, and its actions.
export function TitleHero({
  title,
  details,
  actions,
}: TitleHeroProps): ReactNode {
  const backdrop = backdropUrl(title.backdrop_path);
  return (
    <header
      className={
        backdrop === undefined ? "title-hero" : "title-hero has-backdrop"
      }
      style={
        backdrop === undefined
          ? undefined
          : { backgroundImage: `url("${backdrop}")` }
      }
    >
      <div className="title-hero-inner">
        <div className="title-poster">
          <Poster
            posterPath={title.poster_path}
            title={title.title}
            size="w500"
          />
        </div>
        <div className="title-heading">
          <p>
            <Link to="/requests">Back to discover</Link>
          </p>
          <h1>
            {title.title}
            {title.year === 0 ? null : (
              <span className="title-year"> ({String(title.year)})</span>
            )}
          </h1>
          <TitleMeta details={details} />
        </div>
        <div className="title-actions">{actions}</div>
      </div>
    </header>
  );
}

function TitleMeta({
  details,
}: Readonly<{ details: MetadataDetails }>): ReactNode {
  const runtime = formatMinutes(details.runtime_minutes);
  return (
    <>
      {details.certification === "" && runtime === "" ? null : (
        <p className="title-meta">
          {details.certification === "" ? null : (
            <span className="title-certification">
              <span className="visually-hidden">Rated </span>
              {details.certification}
            </span>
          )}
          {runtime === "" ? null : <span>{runtime}</span>}
        </p>
      )}
      {details.genres.length === 0 ? null : (
        <p className="title-genres">{details.genres.join(", ")}</p>
      )}
    </>
  );
}

export function TrailerLink({
  trailerKey,
}: Readonly<{ trailerKey: string }>): ReactNode {
  if (trailerKey === "") {
    return null;
  }
  return (
    <a
      className="btn"
      href={youTubeUrl(trailerKey)}
      target="_blank"
      rel="noopener noreferrer"
    >
      Watch trailer<span className="visually-hidden"> on YouTube</span>
    </a>
  );
}
