import { describe, expect, it } from "@jest/globals";
import {
  countryFlag,
  formatDollars,
  formatMinutes,
  formatReleaseDay,
  formatScore,
  languageName,
  tmdbCreditsUrl,
  youTubeUrl,
} from "./detail-format.js";

describe("detail formatting", () => {
  it("formats runtime, score, and money as Seerr shows them", () => {
    expect(formatMinutes(119)).toBe("119 minutes");
    expect(formatMinutes(1)).toBe("1 minute");
    expect(formatMinutes(0)).toBe("");
    expect(formatScore(6.64)).toBe("66%");
    expect(formatDollars(37_416_959)).toBe("$37,416,959");
  });

  it("reads a TMDB day in UTC whatever the viewer's zone", () => {
    expect(formatReleaseDay("2026-08-28T00:00:00Z")).toBe("August 28, 2026");
  });

  it("names languages and builds flags only from valid codes", () => {
    expect(languageName("en")).toBe("English");
    expect(countryFlag("US")).toBe("🇺🇸");
    expect(countryFlag("usa")).toBe("");
  });

  it("builds external links with encoded parts", () => {
    expect(youTubeUrl("a&b")).toBe("https://www.youtube.com/watch?v=a%26b");
    expect(tmdbCreditsUrl({ kind: "series", provider_id: "1399" })).toBe(
      "https://www.themoviedb.org/tv/1399/cast",
    );
  });
});
