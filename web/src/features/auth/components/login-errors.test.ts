import { expect, it } from "@jest/globals";
import { ApiError } from "../../../lib/api/errors.js";
import { describeLoginError } from "./login-errors.js";

it("says nothing when there is no error", () => {
  expect(describeLoginError(null)).toBeUndefined();
  expect(describeLoginError(undefined)).toBeUndefined();
});

it.each([
  [401, "The username or password is incorrect."],
  [422, "Check the username and password and try again."],
  [500, "Sign-in failed. Please try again."],
  [503, "Sign-in failed. Please try again."],
])("maps HTTP %d to a fixed sentence", (status, expected) => {
  expect(describeLoginError(new ApiError("http", "raw", { status }))).toBe(
    expected,
  );
});

it("never echoes the server's message", () => {
  const error = new ApiError("http", "stack trace: secret", { status: 500 });

  expect(describeLoginError(error)).not.toContain("secret");
});

it.each([
  [undefined, "Too many sign-in attempts. Please wait a moment and try again."],
  [0, "Too many sign-in attempts. Please wait a moment and try again."],
  [1, "Too many sign-in attempts. Try again in 1 second."],
  [30, "Too many sign-in attempts. Try again in 30 seconds."],
])(
  "describes a rate limit with retry-after %s",
  (retryAfterSeconds, expected) => {
    const error = new ApiError(
      "http",
      "raw",
      retryAfterSeconds === undefined
        ? { status: 429 }
        : { status: 429, retryAfterSeconds },
    );

    expect(describeLoginError(error)).toBe(expected);
  },
);

it("treats an unknown error shape as a generic failure", () => {
  expect(describeLoginError(new Error("boom"))).toBe(
    "Sign-in failed. Please try again.",
  );
  expect(describeLoginError(new ApiError("invalid-response", "bad"))).toBe(
    "Sign-in failed. Please try again.",
  );
});
