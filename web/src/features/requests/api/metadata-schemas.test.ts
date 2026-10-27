import { describe, expect, it } from "@jest/globals";
import {
  createMediaRequestSchema,
  metadataDetailsSchema,
  metadataSeriesSchema,
  metadataTitleDetailSchema,
  providerIdSchema,
  searchQuerySchema,
} from "./metadata-schemas.js";

describe("createMediaRequestSchema", () => {
  const movie = {
    kind: "movie",
    provider_id: "949",
    profile_id: "9c1d2e3f-0000-4000-8000-000000000001",
    seasons: [],
  };

  it("accepts a movie without seasons and a series with some", () => {
    expect(createMediaRequestSchema.parse(movie)).toEqual(movie);
    expect(
      createMediaRequestSchema.safeParse({
        ...movie,
        kind: "series",
        seasons: [1, 3],
      }).success,
    ).toBe(true);
  });

  it("refuses a movie with seasons and a series without", () => {
    expect(
      createMediaRequestSchema.safeParse({ ...movie, seasons: [1] }).success,
    ).toBe(false);
    expect(
      createMediaRequestSchema.safeParse({ ...movie, kind: "series" }).success,
    ).toBe(false);
  });

  it("refuses repeated seasons, specials, and unknown keys", () => {
    expect(
      createMediaRequestSchema.safeParse({
        ...movie,
        kind: "series",
        seasons: [2, 2],
      }).success,
    ).toBe(false);
    expect(
      createMediaRequestSchema.safeParse({
        ...movie,
        kind: "series",
        seasons: [0],
      }).success,
    ).toBe(false);
    expect(
      createMediaRequestSchema.safeParse({ ...movie, extra: true }).success,
    ).toBe(false);
  });
});

describe("providerIdSchema", () => {
  it("accepts decimal ids without a leading zero", () => {
    expect(providerIdSchema.safeParse("949").success).toBe(true);
    expect(providerIdSchema.safeParse("0949").success).toBe(false);
    expect(providerIdSchema.safeParse("9".repeat(21)).success).toBe(false);
    expect(providerIdSchema.safeParse("../x").success).toBe(false);
  });
});

describe("searchQuerySchema", () => {
  it("trims and bounds the query", () => {
    expect(searchQuerySchema.parse("  heat ")).toBe("heat");
    expect(searchQuerySchema.safeParse("   ").success).toBe(false);
    expect(searchQuerySchema.safeParse("x".repeat(201)).success).toBe(false);
  });
});

describe("metadataSeriesSchema", () => {
  it("allows a season without an air date", () => {
    const parsed = metadataSeriesSchema.safeParse({
      kind: "series",
      provider: "tmdb",
      provider_id: "1396",
      title: "The Arrival",
      year: 2021,
      overview: "",
      poster_path: "",
      subscribed: false,
      backdrop_path: "",
      seasons: [{ number: 0, name: "Specials", episode_count: 2 }],
      details: { ...emptyDetails, series: seriesFacts },
    });
    expect(parsed.success).toBe(true);
  });
});

// The server's answer for a title TMDB knows nothing more about.
const emptyDetails = {
  tagline: "",
  status: "",
  certification: "",
  runtime_minutes: 0,
  vote_average: 0,
  vote_count: 0,
  original_language: "",
  imdb_id: "",
  trailer_youtube_key: "",
  budget: 0,
  revenue: 0,
  genres: [],
  keywords: [],
  production_countries: [],
  studios: [],
  release_dates: [],
  watch_providers: [],
  cast: [],
  crew: [],
  recommendations: [],
  similar: [],
};

const seriesFacts = {
  creators: [],
  networks: [{ id: 49, name: "HBO", logo_path: "/hbo.png" }],
  next_episode: { season: 2, episode: 1, name: "" },
  season_count: 1,
  episode_count: 8,
  in_production: true,
};

describe("metadataDetailsSchema", () => {
  it("accepts empty detail and a full movie detail", () => {
    expect(metadataDetailsSchema.safeParse(emptyDetails).success).toBe(true);
    const parsed = metadataDetailsSchema.safeParse({
      ...emptyDetails,
      certification: "R",
      imdb_id: "tt9850370",
      original_language: "en",
      trailer_youtube_key: "official012",
      production_countries: [{ code: "US", name: "United States" }],
      release_dates: [{ type: "digital", date: "2026-10-27T00:00:00Z" }],
      cast: [{ person_id: 1, name: "Actor", role: "", profile_path: "" }],
    });
    expect(parsed.success).toBe(true);
  });

  it("rejects values the contract does not allow", () => {
    for (const bad of [
      { imdb_id: "nm0000001" },
      { trailer_youtube_key: "a/b" },
      { original_language: "EN" },
      { vote_average: 11 },
      { release_dates: [{ type: "streaming", date: "2026-10-27T00:00:00Z" }] },
      { production_countries: [{ code: "usa", name: "United States" }] },
      { cast: [{ person_id: 0, name: "Actor", role: "", profile_path: "" }] },
      {
        genres: Array.from(
          { length: 11 },
          (_, index) => `Genre ${String(index)}`,
        ),
      },
    ]) {
      expect(
        metadataDetailsSchema.safeParse({ ...emptyDetails, ...bad }).success,
      ).toBe(false);
    }
  });

  it("requires the detail on a title page's answer", () => {
    const title = {
      kind: "movie",
      provider: "tmdb",
      provider_id: "949",
      title: "Heat",
      year: 1995,
      overview: "",
      poster_path: "",
      backdrop_path: "",
      subscribed: false,
    };
    expect(metadataTitleDetailSchema.safeParse(title).success).toBe(false);
    expect(
      metadataTitleDetailSchema.safeParse({ ...title, details: emptyDetails })
        .success,
    ).toBe(true);
  });
});
