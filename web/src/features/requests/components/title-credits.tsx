import { useId, useState, type ReactNode } from "react";
import type { MetadataCredit, MetadataTitle } from "../api/metadata-schemas.js";
import { tmdbCreditsUrl, tmdbImageUrl } from "./detail-format.js";

type CrewGridProps = Readonly<{
  crew: readonly MetadataCredit[];
  title: Pick<MetadataTitle, "kind" | "provider_id">;
}>;

// The key crew, job over name, with a link to TMDB's full credits.
export function CrewGrid({ crew, title }: CrewGridProps): ReactNode {
  if (crew.length === 0) {
    return null;
  }
  return (
    <div className="title-crew">
      <dl>
        {crew.map((member) => (
          <div key={`${String(member.person_id)}-${member.role}`}>
            <dt>{member.role}</dt>
            <dd>{member.name}</dd>
          </div>
        ))}
      </dl>
      <a
        href={tmdbCreditsUrl(title)}
        target="_blank"
        rel="noopener noreferrer"
        className="title-crew-more"
      >
        View full crew<span className="visually-hidden"> on TMDB</span>
      </a>
    </div>
  );
}

export function KeywordChips({
  keywords,
}: Readonly<{ keywords: readonly string[] }>): ReactNode {
  if (keywords.length === 0) {
    return null;
  }
  return (
    <ul className="title-keywords" aria-label="Keywords">
      {keywords.map((keyword, index) => (
        <li key={`${String(index)}-${keyword}`}>{keyword}</li>
      ))}
    </ul>
  );
}

// The cast in billing order, scrolling sideways like a discover row.
export function CastRow({
  cast,
}: Readonly<{ cast: readonly MetadataCredit[] }>): ReactNode {
  const id = useId();
  if (cast.length === 0) {
    return null;
  }
  return (
    <section aria-labelledby={id} className="discover-row">
      <h2 id={id}>Cast</h2>
      <div className="discover-scroller">
        <ul className="discover-list cast-list" aria-label="Cast">
          {/* TMDB may bill one person twice, once per character. */}
          {cast.map((member, index) => (
            <li key={`${String(index)}-${String(member.person_id)}`}>
              <CastCard member={member} />
            </li>
          ))}
        </ul>
      </div>
    </section>
  );
}

function CastCard({ member }: Readonly<{ member: MetadataCredit }>): ReactNode {
  return (
    <figure className="cast-card">
      <ProfilePhoto path={member.profile_path} />
      <figcaption>
        <span className="cast-card-name">{member.name}</span>
        {member.role === "" ? null : (
          <span className="cast-card-role">{member.role}</span>
        )}
      </figcaption>
    </figure>
  );
}

// A profile photo, or a silhouette when TMDB has none or it fails to load.
// The name beside it says who it is, so the image is decorative.
function ProfilePhoto({ path }: Readonly<{ path: string }>): ReactNode {
  const [failed, setFailed] = useState(false);
  if (path === "" || failed) {
    return (
      <span className="cast-photo cast-photo-placeholder" aria-hidden="true">
        <svg viewBox="0 0 24 24" focusable="false">
          <path d="M12 12a4 4 0 1 0 0-8 4 4 0 0 0 0 8Zm-7 8a7 7 0 0 1 14 0" />
        </svg>
      </span>
    );
  }
  return (
    <img
      className="cast-photo"
      src={tmdbImageUrl(path, "w185")}
      alt=""
      loading="lazy"
      decoding="async"
      onError={() => {
        setFailed(true);
      }}
    />
  );
}
