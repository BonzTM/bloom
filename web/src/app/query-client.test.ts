import { expect, it } from "@jest/globals";
import { ApiError } from "../lib/api/errors.js";
import { createQueryClient } from "./query-client.js";

// The production retry policy, read from the client's own defaults: network
// failures are retried a bounded number of times, and nothing the server
// answered deliberately is retried at all.
function retry(failureCount: number, error: Error): boolean {
  const policy = createQueryClient().getDefaultOptions().queries?.retry;
  if (typeof policy !== "function") {
    throw new Error("retry policy must be a function");
  }
  return policy(failureCount, error);
}

const network = new ApiError("network", "unreachable");

it("retries network failures twice and then stops", () => {
  expect(retry(0, network)).toBe(true);
  expect(retry(1, network)).toBe(true);
  expect(retry(2, network)).toBe(false);
});

it.each([401, 403, 404, 422, 429, 500])(
  "never retries an HTTP %i",
  (status) => {
    const error = new ApiError("http", "answered", { status });
    expect(retry(0, error)).toBe(false);
  },
);

it("never retries an invalid or aborted response", () => {
  expect(retry(0, new ApiError("invalid-response", "bad body"))).toBe(false);
  expect(retry(0, new ApiError("aborted", "cancelled"))).toBe(false);
});
