import { describe, expect, it } from "@jest/globals";
import {
  createMediaRequestSchema,
  metadataSeriesSchema,
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
      seasons: [{ number: 0, name: "Specials", episode_count: 2 }],
    });
    expect(parsed.success).toBe(true);
  });
});
