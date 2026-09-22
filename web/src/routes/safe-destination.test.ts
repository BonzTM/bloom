import { expect, it } from "@jest/globals";
import { safeDestination } from "./safe-destination.js";

const ORIGIN = "https://bloom.example";

it.each([
  ["/users/42", "/users/42"],
  ["/stats?range=7d#top", "/stats?range=7d#top"],
])("keeps an in-app path %s", (from, expected) => {
  expect(safeDestination({ from }, ORIGIN)).toBe(expected);
});

it.each([
  ["absent state", undefined],
  ["non-object state", "/x"],
  ["non-string from", { from: 42 }],
  ["relative path", { from: "users" }],
  ["external URL", { from: "https://evil.example/" }],
  ["protocol-relative", { from: "//evil.example/" }],
  ["backslash authority", { from: "/\\evil.example" }],
  ["control character", { from: "/x\r\nSet-Cookie: a=b" }],
  ["the sign-in page itself", { from: "/login?next=1" }],
])("falls back to home for %s", (_label, state) => {
  expect(safeDestination(state, ORIGIN)).toBe("/");
});
