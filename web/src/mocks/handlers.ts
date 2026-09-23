import { http, HttpResponse, type HttpResponseResolver } from "msw";
import { z } from "zod/v4";
import {
  knownPermissions,
  loginRequestSchema,
  type Account,
  type KnownPermission,
  type Session,
} from "../features/auth/api/auth-schemas.js";
import type { Role } from "../features/roles/api/roles-schemas.js";
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

// Roles the mock server lists: the built-in ones plus enough custom roles
// that the contract's default page of 50 is exercised by the real client
// request. Cursors are the offset of the next page.
const DEFAULT_PAGE_SIZE = 50;
const MAX_PAGE_SIZE = 100;
const MAX_CURSOR_LENGTH = 86;
const MAX_INT64 = 9_223_372_036_854_775_807n;
const CUSTOM_ROLE_COUNT = 49;

function customRole(index: number): Role {
  const ordinal = String(index).padStart(2, "0");
  return {
    id: `8e1c2f7a-0000-4000-8000-0000000001${ordinal}`,
    name: `custom-${ordinal}`,
    description: `Custom role ${ordinal}.`,
    built_in: false,
    created_at: "2026-09-10T08:30:00Z",
    permissions: ["requests.read.own"],
  };
}

// Ordered by name, as the server orders them.
export const mockRoles: readonly Role[] = [
  {
    id: "8e1c2f7a-0000-4000-8000-000000000001",
    name: "admin",
    description: "Full access to every part of Bloom.",
    built_in: true,
    created_at: "2026-09-01T12:00:00Z",
    permissions: [...allPermissions],
  },
  ...Array.from({ length: CUSTOM_ROLE_COUNT }, (_, index) =>
    customRole(index + 1),
  ),
  {
    id: "8e1c2f7a-0000-4000-8000-000000000002",
    name: "member",
    description: "Request media and see their own statistics.",
    built_in: true,
    created_at: "2026-09-01T12:00:00Z",
    permissions: ["requests.create", "requests.read.own", "stats.read.own"],
  },
  {
    id: "8e1c2f7a-0000-4000-8000-000000000003",
    name: "reviewer",
    description: "Approve requests.",
    built_in: false,
    created_at: "2026-09-10T08:30:00Z",
    permissions: ["requests.approve", "requests.read.own"],
  },
];

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

const rolesQuerySchema = z.object({
  cursor: z
    .string()
    .min(1)
    .max(MAX_CURSOR_LENGTH)
    .regex(/^offset:\d{1,3}$/)
    .optional(),
  // Decimal digits only, as the server's integer parser reads them: no
  // sign, exponent, radix prefix, fraction, or whitespace; leading zeros are
  // fine and nothing beyond a signed 64-bit integer. Values above the
  // maximum page are clamped.
  page_size: z
    .string()
    .regex(/^[0-9]+$/)
    .refine((digits) => /^[0-9]+$/.test(digits) && BigInt(digits) <= MAX_INT64)
    .transform((digits) => Math.min(Number(digits), MAX_PAGE_SIZE))
    .pipe(z.number().int().min(1))
    .optional(),
});

// The parameters the server reads, each at most once as the server requires;
// unknown parameters are ignored, as the server ignores them.
function singleValues(
  params: URLSearchParams,
): Record<string, string> | undefined {
  const values: Record<string, string> = {};
  for (const name of ["cursor", "page_size"]) {
    const all = params.getAll(name);
    if (all.length > 1) {
      return undefined;
    }
    if (all[0] !== undefined) {
      values[name] = all[0];
    }
  }
  return values;
}

function rolesPage(url: URL) {
  const raw = singleValues(url.searchParams);
  const query = rolesQuerySchema.safeParse(raw);
  if (raw === undefined || !query.success) {
    return envelope(422, "validation_failed", "invalid cursor or page_size");
  }
  const offset =
    query.data.cursor === undefined
      ? 0
      : Number(query.data.cursor.slice("offset:".length));
  const size = query.data.page_size ?? DEFAULT_PAGE_SIZE;
  const items = mockRoles.slice(offset, offset + size);
  const next = offset + size;
  return HttpResponse.json({
    items,
    next_cursor: next < mockRoles.length ? `offset:${String(next)}` : "",
  });
}

export const handlers = [
  http.get(
    "*/api/v1/roles",
    jsonApi(({ request }) => {
      if (!signedIn) {
        return envelope(401, "unauthorized", "sign in required");
      }
      if (!granted.includes("admin.roles")) {
        return envelope(403, "forbidden", "missing permission admin.roles");
      }
      return rolesPage(new URL(request.url));
    }),
  ),
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
