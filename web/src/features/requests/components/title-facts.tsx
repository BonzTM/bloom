import type { ReactNode } from "react";
import type {
  MetadataCompany,
  MetadataDetails,
  MetadataSeriesFacts,
  MetadataTitle,
  TitleRatings,
} from "../api/metadata-schemas.js";
import {
  countryFlag,
  formatDollars,
  formatReleaseDay,
  imdbUrl,
  languageName,
  releaseTypeLabel,
  tmdbImageUrl,
  tmdbUrl,
} from "./detail-format.js";
import { TitleScores } from "./title-scores.js";

type TitleFactsProps = Readonly<{
  title: Pick<MetadataTitle, "kind" | "provider_id">;
  details: MetadataDetails;
  ratings: TitleRatings | undefined;
}>;

// The side card of facts a Seerr title page shows: the scores, then one
// row per fact TMDB knows, then where it streams and where to read more.
export function TitleFacts({
  title,
  details,
  ratings,
}: TitleFactsProps): ReactNode {
  return (
    <aside className="card title-facts" aria-label="Facts">
      <TitleScores details={details} ratings={ratings} />
      <dl>
        <Fact label="Status">{details.status}</Fact>
        <MovieFacts details={details} />
        {details.series === undefined ? null : (
          <SeriesFacts facts={details.series} />
        )}
        <Fact label="Original language">
          {details.original_language === ""
            ? ""
            : languageName(details.original_language)}
        </Fact>
        <Fact label="Production country">
          {details.production_countries.map((country) => (
            <span key={country.code} className="title-fact-line">
              <span aria-hidden="true">{countryFlag(country.code)} </span>
              {country.name}
            </span>
          ))}
        </Fact>
        <Fact label="Studios">{lines(details.studios)}</Fact>
      </dl>
      <WatchProviders providers={details.watch_providers} />
      <ExternalLinks title={title} imdbId={details.imdb_id} />
    </aside>
  );
}

function MovieFacts({
  details,
}: Readonly<{ details: MetadataDetails }>): ReactNode {
  return (
    <>
      <Fact label="Release dates">
        {details.release_dates.map((release) => (
          <span key={release.type} className="title-fact-line">
            {formatReleaseDay(release.date)}{" "}
            <span className="title-fact-note">
              ({releaseTypeLabel(release.type)})
            </span>
          </span>
        ))}
      </Fact>
      <Fact label="Revenue">
        {details.revenue > 0 ? formatDollars(details.revenue) : ""}
      </Fact>
      <Fact label="Budget">
        {details.budget > 0 ? formatDollars(details.budget) : ""}
      </Fact>
    </>
  );
}

function SeriesFacts({
  facts,
}: Readonly<{ facts: MetadataSeriesFacts }>): ReactNode {
  const next = facts.next_episode;
  return (
    <>
      <Fact label="Seasons">
        {facts.season_count > 0 ? String(facts.season_count) : ""}
      </Fact>
      <Fact label="Episodes">
        {facts.episode_count > 0 ? String(facts.episode_count) : ""}
      </Fact>
      <Fact label="First aired">
        {facts.first_air_date === undefined
          ? ""
          : formatReleaseDay(facts.first_air_date)}
      </Fact>
      <Fact label="Last aired">
        {facts.last_air_date === undefined
          ? ""
          : formatReleaseDay(facts.last_air_date)}
      </Fact>
      <Fact label="Next episode">
        {next === undefined ? "" : nextEpisodeLabel(next)}
      </Fact>
      <Fact label="Networks">{lines(facts.networks)}</Fact>
    </>
  );
}

function nextEpisodeLabel(
  next: NonNullable<MetadataSeriesFacts["next_episode"]>,
): string {
  const code = `S${String(next.season)} E${String(next.episode)}`;
  const day =
    next.air_date === undefined ? "" : `, ${formatReleaseDay(next.air_date)}`;
  return next.name === "" ? `${code}${day}` : `${code} “${next.name}”${day}`;
}

function lines(companies: readonly MetadataCompany[]): ReactNode[] {
  return companies.map((company) => (
    <span key={company.id} className="title-fact-line">
      {company.name}
    </span>
  ));
}

// One fact row, or nothing when there is nothing to say.
function Fact({
  label,
  children,
}: Readonly<{ label: string; children: ReactNode }>): ReactNode {
  if (children === "" || (Array.isArray(children) && children.length === 0)) {
    return null;
  }
  return (
    <div className="title-fact">
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  );
}

function WatchProviders({
  providers,
}: Readonly<{ providers: MetadataDetails["watch_providers"] }>): ReactNode {
  if (providers.length === 0) {
    return null;
  }
  return (
    <div className="title-providers">
      <h2>Streaming on</h2>
      <ul>
        {providers.map((provider) => (
          <li key={provider.id}>
            {provider.logo_path === "" ? (
              <span className="badge badge-neutral">{provider.name}</span>
            ) : (
              <img
                src={tmdbImageUrl(provider.logo_path, "w92")}
                alt={provider.name}
                title={provider.name}
                loading="lazy"
                decoding="async"
              />
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}

function ExternalLinks({
  title,
  imdbId,
}: Readonly<{
  title: Pick<MetadataTitle, "kind" | "provider_id">;
  imdbId: string;
}>): ReactNode {
  return (
    <p className="title-links">
      <a href={tmdbUrl(title)} target="_blank" rel="noopener noreferrer">
        TMDB
      </a>
      {imdbId === "" ? null : (
        <a href={imdbUrl(imdbId)} target="_blank" rel="noopener noreferrer">
          IMDb
        </a>
      )}
    </p>
  );
}
