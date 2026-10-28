import type {
  MetadataReleaseType,
  MetadataTitle,
} from "../api/metadata-schemas.js";

// TMDB serves every image Bloom shows from one host, the only image origin
// the SPA's content security policy allows.
const IMAGE_ORIGIN = "https://image.tmdb.org/t/p";

export type ImageSize = "w92" | "w185";

export function tmdbImageUrl(path: string, size: ImageSize): string {
  return `${IMAGE_ORIGIN}/${size}${path}`;
}

const RELEASE_LABELS: Readonly<Record<MetadataReleaseType, string>> = {
  premiere: "Premiere",
  theatrical_limited: "Limited theatrical",
  theatrical: "Theatrical",
  digital: "Digital",
  physical: "Physical",
  tv: "TV",
};

export function releaseTypeLabel(type: MetadataReleaseType): string {
  return RELEASE_LABELS[type];
}

// "119 minutes", or nothing when TMDB does not know.
export function formatMinutes(minutes: number): string {
  if (minutes <= 0) {
    return "";
  }
  return minutes === 1 ? "1 minute" : `${String(minutes)} minutes`;
}

// TMDB's 0–10 average as Seerr shows it: a whole percentage.
export function formatScore(voteAverage: number): string {
  return `${String(Math.round(voteAverage * 10))}%`;
}

const VOTES = new Intl.NumberFormat("en-US", {
  notation: "compact",
  maximumFractionDigits: 1,
});

// A vote count such as 2,123,456 written as "2.1M".
export function formatVotes(votes: number): string {
  return VOTES.format(votes);
}

const DOLLARS = new Intl.NumberFormat("en-US", {
  style: "currency",
  currency: "USD",
  maximumFractionDigits: 0,
});

export function formatDollars(amount: number): string {
  return DOLLARS.format(amount);
}

const DAY = new Intl.DateTimeFormat("en-US", {
  dateStyle: "long",
  timeZone: "UTC",
});

// "August 28, 2026". TMDB dates are calendar days, so they read in UTC.
export function formatReleaseDay(value: string): string {
  return DAY.format(new Date(value));
}

const LANGUAGES = new Intl.DisplayNames(["en"], { type: "language" });

// "English" for "en"; the code itself when the runtime does not know it.
export function languageName(code: string): string {
  try {
    return LANGUAGES.of(code) ?? code;
  } catch {
    return code;
  }
}

// The flag emoji for an ISO 3166-1 alpha-2 code, built from regional
// indicator symbols, or nothing for anything else.
export function countryFlag(code: string): string {
  if (!/^[A-Z]{2}$/.test(code)) {
    return "";
  }
  const base = 0x1f1e6 - "A".charCodeAt(0);
  return String.fromCodePoint(
    base + code.charCodeAt(0),
    base + code.charCodeAt(1),
  );
}

export function youTubeUrl(key: string): string {
  return `https://www.youtube.com/watch?v=${encodeURIComponent(key)}`;
}

export function imdbUrl(id: string): string {
  return `https://www.imdb.com/title/${encodeURIComponent(id)}/`;
}

const TMDB_SEGMENTS: Readonly<Record<MetadataTitle["kind"], string>> = {
  movie: "movie",
  series: "tv",
};

export function tmdbUrl(title: Pick<MetadataTitle, "kind" | "provider_id">) {
  return `https://www.themoviedb.org/${TMDB_SEGMENTS[title.kind]}/${encodeURIComponent(title.provider_id)}`;
}

// TMDB's full cast and crew page, which Bloom links instead of copying.
export function tmdbCreditsUrl(
  title: Pick<MetadataTitle, "kind" | "provider_id">,
): string {
  return `${tmdbUrl(title)}/cast`;
}
