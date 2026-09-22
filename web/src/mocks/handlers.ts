import { http, HttpResponse } from "msw";
import {
  loginInputSchema,
  type Account,
} from "../features/auth/api/auth-schemas.js";
import type { VersionInfo } from "../features/system/api/system-schemas.js";

export const mockVersion: VersionInfo = {
  name: "bloom",
  version: "0.1.0-dev",
  commit: "0123456789abcdef0123456789abcdef01234567",
};

export const mockAccount: Account = {
  id: "0b6c3d2e-1111-4a2b-9c3d-000000000001",
  username: "admin",
};

// Credentials the mock backend accepts. Anything else is a 401, and the
// reserved username below is always rate limited so that UI path is testable.
export const mockCredentials = { username: "admin", password: "correct horse" };
export const RATE_LIMITED_USERNAME = "locked";
export const RATE_LIMIT_RETRY_AFTER_SECONDS = 30;

// One process-wide mock session so `/me` reflects an earlier `/login`. It is a
// stand-in for the cookie the real backend sets; tests reset it between cases.
// It cannot prove cookie or CSRF behaviour, which the backend suite covers.
let signedIn = false;

export function resetMockSession(): void {
  signedIn = false;
}

export function signInMockSession(): void {
  signedIn = true;
}

export function envelope(
  status: number,
  code: string,
  message: string,
  headers: HeadersInit = {},
) {
  return HttpResponse.json(
    { code, message, request_id: "req-mock" },
    { status, headers },
  );
}

function isJsonRequest(request: Request): boolean {
  return (
    request.headers.get("content-type")?.includes("application/json") === true
  );
}

export const handlers = [
  http.get("*/api/v1/version", () => HttpResponse.json(mockVersion)),
  http.get("*/api/v1/auth/me", () =>
    signedIn
      ? HttpResponse.json({ account: mockAccount })
      : envelope(401, "unauthenticated", "sign in required"),
  ),
  http.post("*/api/v1/auth/login", async ({ request }) => {
    if (!isJsonRequest(request)) {
      return envelope(415, "unsupported_media_type", "expected JSON");
    }
    const input = loginInputSchema.safeParse(await request.json());
    if (!input.success) {
      return envelope(422, "validation_failed", "invalid input");
    }
    if (input.data.username === RATE_LIMITED_USERNAME) {
      return envelope(429, "rate_limited", "too many attempts", {
        "retry-after": String(RATE_LIMIT_RETRY_AFTER_SECONDS),
      });
    }
    if (
      input.data.username !== mockCredentials.username ||
      input.data.password !== mockCredentials.password
    ) {
      return envelope(401, "invalid_credentials", "invalid credentials");
    }
    signedIn = true;
    return HttpResponse.json({ account: mockAccount });
  }),
  http.post("*/api/v1/auth/logout", () => {
    if (!signedIn) {
      return envelope(401, "unauthenticated", "sign in required");
    }
    signedIn = false;
    return new HttpResponse(null, { status: 204 });
  }),
];
