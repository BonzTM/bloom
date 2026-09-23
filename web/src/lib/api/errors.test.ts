import { expect, it } from "@jest/globals";
import { accessDenial, ApiError, mapHttpError } from "./errors.js";

it("reads a 401 as a lost session and a 403 as a missing right", () => {
  expect(accessDenial(new ApiError("http", "no", { status: 401 }))).toBe(
    "unauthenticated",
  );
  expect(
    accessDenial(
      mapHttpError(403, { code: "forbidden", message: "missing permission" }),
    ),
  ).toBe("forbidden");
});

it("does not read a cross-site rejection as a denial", () => {
  const rejected = mapHttpError(403, {
    code: "csrf_rejected",
    message: "cross-site request",
  });
  expect(accessDenial(rejected)).toBeUndefined();
});

it("ignores other failures", () => {
  expect(accessDenial(new ApiError("network", "down"))).toBeUndefined();
  expect(accessDenial(new Error("plain"))).toBeUndefined();
  expect(accessDenial(undefined)).toBeUndefined();
});
