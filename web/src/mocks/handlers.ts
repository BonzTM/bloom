import { http, HttpResponse, type HttpResponseResolver } from "msw";
import { z } from "zod/v4";
import {
  knownPermissions,
  loginRequestSchema,
  type Account,
  type KnownPermission,
  type Session,
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

// The server's permission catalog in the sorted order `/me` answers with.
export const allPermissions: readonly KnownPermission[] = [
  ...knownPermissions,
].sort();

export const mockSession: Session = {
  account: mockAccount,
  roles: ["admin"],
  permissions: [...allPermissions],
};

// Credentials the mock backend accepts. Anything else is a 401, and the
// username "locked" is always rate limited so that UI path is testable.
export const mockCredentials = { username: "admin", password: "correct horse" };
const RATE_LIMITED_USERNAME = "locked";
const RATE_LIMIT_RETRY_AFTER_SECONDS = 30;

// One process-wide mock session so `/me` reflects an earlier `/login`. It is a
// stand-in for the cookie the real backend sets; tests reset it between cases.
// It cannot prove cookie or CSRF behaviour, which the backend suite covers.
let signedIn = false;
// What the signed-in mock account may do; tests narrow it to exercise the
// permission gates.
let granted: readonly KnownPermission[] = allPermissions;

export function resetMockSession(): void {
  signedIn = false;
  granted = allPermissions;
}

export function setMockPermissions(next: readonly KnownPermission[]): void {
  granted = next;
}

function currentSession(): Session {
  return { ...mockSession, permissions: [...granted] };
}

export function signInMockSession(): void {
  signedIn = true;
}

// `ErrorCode` from api/openapi.yaml: the only codes the mock may answer.
export const errorCodeSchema = z.enum([
  "not_found",
  "already_exists",
  "invalid_argument",
  "unavailable",
  "internal",
  "invalid_credentials",
  "unauthorized",
  "validation_failed",
  "rate_limited",
  "csrf_rejected",
  "unsupported_media_type",
  "method_not_allowed",
  "forbidden",
]);

export type ErrorCode = z.output<typeof errorCodeSchema>;

export function envelope(
  status: number,
  code: ErrorCode,
  message: string,
  headers: HeadersInit = {},
) {
  return HttpResponse.json(
    { code, message, request_id: "req-mock" },
    { status, headers },
  );
}

// The client promises to ask for JSON on every request. Every JSON handler,
// default or test override, goes through this wrapper so a regression in the
// fetch boundary fails the test directly rather than as a server answer.
export function jsonApi(resolver: HttpResponseResolver): HttpResponseResolver {
  return (info) => {
    const accept = info.request.headers.get("accept");
    if (accept?.includes("application/json") !== true) {
      throw new Error("mock server: the request did not ask for JSON");
    }
    return resolver(info);
  };
}

function sendsJson(request: Request): boolean {
  return (
    request.headers.get("content-type")?.includes("application/json") === true
  );
}

export const handlers = [
  http.get(
    "*/api/v1/version",
    jsonApi(() => HttpResponse.json(mockVersion)),
  ),
  http.get(
    "*/api/v1/auth/me",
    jsonApi(() =>
      signedIn
        ? HttpResponse.json(currentSession())
        : envelope(401, "unauthorized", "sign in required"),
    ),
  ),
  http.post(
    "*/api/v1/auth/login",
    jsonApi(async ({ request }) => {
      if (!sendsJson(request)) {
        return envelope(415, "unsupported_media_type", "expected JSON");
      }
      const input = loginRequestSchema.safeParse(await request.json());
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
      return HttpResponse.json(currentSession());
    }),
  ),
  http.post(
    "*/api/v1/auth/logout",
    jsonApi(async ({ request }) => {
      if ((await request.text()).length !== 0) {
        return envelope(400, "invalid_argument", "unexpected body");
      }
      if (!signedIn) {
        return envelope(401, "unauthorized", "sign in required");
      }
      signedIn = false;
      return new HttpResponse(null, { status: 204 });
    }),
  ),
];
