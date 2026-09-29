import { describe, expect, it } from "@jest/globals";
import { ApiError, mapHttpError } from "../../../lib/api/errors.js";
import { describeRegisterError } from "./media-server-errors.js";

function probeFailure(reason?: string) {
  return mapHttpError(502, {
    code: "media_server_failure",
    message: "media server probe failed",
    request_id: "req-1",
    ...(reason === undefined ? {} : { reason }),
  });
}

describe("describeRegisterError", () => {
  it("says which way the probe failed when the API says so", () => {
    expect(describeRegisterError(probeFailure("unreachable"))).toMatch(
      /could not reach the server/,
    );
    expect(describeRegisterError(probeFailure("unauthorized"))).toMatch(
      /rejected the API key/,
    );
    expect(describeRegisterError(probeFailure("not_found"))).toMatch(
      /API was not found/,
    );
    expect(describeRegisterError(probeFailure("malformed"))).toMatch(
      /not like a Jellyfin server/,
    );
  });

  it("falls back to a neutral sentence without a reason or with an unknown one", () => {
    expect(describeRegisterError(probeFailure())).toBe(
      "The server could not be checked. Nothing was saved.",
    );
    expect(describeRegisterError(probeFailure("teapot"))).toBe(
      "The server could not be checked. Nothing was saved.",
    );
  });
});

it("reports a probe that outlives the browser's wait as an unknown outcome", () => {
  expect(
    describeRegisterError(new ApiError("aborted", "The request was cancelled")),
  ).toMatch(/outcome is unknown/);
});
