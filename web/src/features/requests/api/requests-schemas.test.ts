import { describe, expect, it } from "@jest/globals";
import {
  mediaRequestSchema,
  metadataKeyRequestSchema,
  requestDecisionSchema,
  requestProfileInputSchema,
} from "./requests-schemas.js";

const profile = {
  name: "Movies HD",
  kinds: ["movie"],
  download_manager_kind: "radarr",
  download_manager_instance: "radarr-main",
  quality_profile: "HD-1080p",
  root_folder: "/data/movies",
  tags: ["bloom"],
};

describe("requestProfileInputSchema", () => {
  it("accepts a complete profile", () => {
    expect(requestProfileInputSchema.parse(profile)).toEqual(profile);
  });

  it("refuses keys the contract does not name", () => {
    expect(
      requestProfileInputSchema.safeParse({ ...profile, id: "x" }).success,
    ).toBe(false);
  });

  it("needs at least one kind and no repeats", () => {
    expect(
      requestProfileInputSchema.safeParse({ ...profile, kinds: [] }).success,
    ).toBe(false);
    expect(
      requestProfileInputSchema.safeParse({
        ...profile,
        kinds: ["movie", "movie"],
      }).success,
    ).toBe(false);
  });

  it("refuses untrimmed values and repeated tags", () => {
    expect(
      requestProfileInputSchema.safeParse({ ...profile, name: " x " }).success,
    ).toBe(false);
    expect(
      requestProfileInputSchema.safeParse({ ...profile, tags: ["a", "a"] })
        .success,
    ).toBe(false);
  });

  it("bounds the name by bytes, not characters", () => {
    expect(
      requestProfileInputSchema.safeParse({ ...profile, name: "é".repeat(50) })
        .success,
    ).toBe(true);
    expect(
      requestProfileInputSchema.safeParse({ ...profile, name: "é".repeat(51) })
        .success,
    ).toBe(false);
  });
});

describe("requestDecisionSchema", () => {
  it("allows no reason, and a trimmed bounded one", () => {
    expect(requestDecisionSchema.parse({})).toEqual({});
    expect(requestDecisionSchema.parse({ reason: "ok" })).toEqual({
      reason: "ok",
    });
    expect(requestDecisionSchema.safeParse({ reason: " ok" }).success).toBe(
      false,
    );
    expect(
      requestDecisionSchema.safeParse({ reason: "x".repeat(1001) }).success,
    ).toBe(false);
  });
});

describe("metadataKeyRequestSchema", () => {
  it("refuses empty keys and control characters", () => {
    expect(metadataKeyRequestSchema.safeParse({ api_key: "" }).success).toBe(
      false,
    );
    expect(
      metadataKeyRequestSchema.safeParse({ api_key: "abc\ndef" }).success,
    ).toBe(false);
    expect(metadataKeyRequestSchema.parse({ api_key: "abc" })).toEqual({
      api_key: "abc",
    });
  });
});

describe("mediaRequestSchema", () => {
  const request = {
    id: "5e4d3c2b-0000-4000-8000-000000000002",
    kind: "movie",
    provider: "tmdb",
    provider_id: "949",
    title: "Heat",
    year: 1995,
    poster_path: "/heat.jpg",
    requester_account_id: "0b6c3d2e-1111-4a2b-9c3d-000000000002",
    profile_id: "9c1d2e3f-0000-4000-8000-000000000001",
    status: "pending",
    seasons: [],
    decision_reason: "",
    failure_reason: "",
    download_manager_item_id: "",
    decided_by_account_id: "",
    created_at: "2026-09-22T09:00:00Z",
    updated_at: "2026-09-22T09:00:00Z",
  };

  it("strips fields the contract does not name yet", () => {
    expect(mediaRequestSchema.parse({ ...request, extra: 1 })).toEqual(request);
  });

  it("refuses a status outside the lifecycle", () => {
    expect(
      mediaRequestSchema.safeParse({ ...request, status: "lost" }).success,
    ).toBe(false);
  });
});
