import { expect, it } from "@jest/globals";
import { readPublicConfig } from "./config.js";

const ORIGIN = "https://bloom.example";

it("defaults the API base URL to the page origin", () => {
  const config = readPublicConfig({ DEV: false }, ORIGIN);

  expect(config).toEqual({ apiBaseUrl: `${ORIGIN}/`, enableMsw: false });
});

it("accepts an explicit API base URL on the same origin", () => {
  const config = readPublicConfig(
    { DEV: true, VITE_API_BASE_URL: `${ORIGIN}/api-root/` },
    ORIGIN,
  );

  expect(config.apiBaseUrl).toBe(`${ORIGIN}/api-root/`);
});

it("rejects an API base URL on another origin", () => {
  expect(() =>
    readPublicConfig(
      { VITE_API_BASE_URL: "https://elsewhere.example/" },
      ORIGIN,
    ),
  ).toThrow(/same-origin/);
});

it("enables the mock worker only in development builds", () => {
  expect(
    readPublicConfig({ DEV: true, VITE_ENABLE_MSW: "true" }, ORIGIN).enableMsw,
  ).toBe(true);
  expect(
    readPublicConfig({ DEV: false, VITE_ENABLE_MSW: "true" }, ORIGIN).enableMsw,
  ).toBe(false);
});
