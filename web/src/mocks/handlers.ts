import { http, HttpResponse, type HttpResponseResolver } from "msw";
import { z } from "zod/v4";
import {
  knownPermissions,
  loginRequestSchema,
  type Account,
  type KnownPermission,
  type Session,
} from "../features/auth/api/auth-schemas.js";
import {
  registerMediaServerRequestSchema,
  type MediaServer,
  type RegisterMediaServerRequest,
} from "../features/media-servers/api/media-servers-schemas.js";
import {
  acceptInviteRequestSchema,
  createInviteRequestSchema,
  INVITE_CODE_PATTERN,
  type Invite,
  type CreateInviteRequest,
} from "../features/invites/api/invites-schemas.js";
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
const MAX_MEDIA_SERVER_CURSOR_LENGTH = 400;
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

// Sign-in methods the mock server advertises. Local only by default; a test
// enables single sign-on with `setMockProviders`.
export const localProvider = { id: "local", display_name: "Password" } as const;
export const oidcProvider = {
  id: "oidc",
  display_name: "Homelab SSO",
} as const;
let providers: readonly { id: "local" | "oidc"; display_name: string }[] = [
  localProvider,
];

export function setMockProviders(
  next: readonly { id: "local" | "oidc"; display_name: string }[],
): void {
  providers = next;
}

export function resetMockSession(): void {
  signedIn = false;
  granted = allPermissions;
  providers = [localProvider];
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
  "media_server_failure",
  "username_unavailable",
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

// Cursors are the offset of the next page; page_size is decimal digits only,
// as the server's integer parser reads them: no sign, exponent, radix prefix,
// fraction, or whitespace; leading zeros are fine and nothing beyond a signed
// 64-bit integer. Values above the maximum page are clamped.
function pageQuerySchema(maxCursorLength: number) {
  return z.object({
    cursor: z
      .string()
      .min(1)
      .max(maxCursorLength)
      .regex(/^offset:\d{1,3}$/)
      .optional(),
    page_size: z
      .string()
      .regex(/^[0-9]+$/)
      .refine(
        (digits) => /^[0-9]+$/.test(digits) && BigInt(digits) <= MAX_INT64,
      )
      .transform((digits) => Math.min(Number(digits), MAX_PAGE_SIZE))
      .pipe(z.number().int().min(1))
      .optional(),
  });
}

const rolesQuerySchema = pageQuerySchema(MAX_CURSOR_LENGTH);
const mediaServersQuerySchema = pageQuerySchema(MAX_MEDIA_SERVER_CURSOR_LENGTH);

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

function pagedItems(
  url: URL,
  schema: ReturnType<typeof pageQuerySchema>,
  items: readonly unknown[],
) {
  const raw = singleValues(url.searchParams);
  const query = schema.safeParse(raw);
  if (raw === undefined || !query.success) {
    return envelope(422, "validation_failed", "invalid cursor or page_size");
  }
  const offset =
    query.data.cursor === undefined
      ? 0
      : Number(query.data.cursor.slice("offset:".length));
  const size = query.data.page_size ?? DEFAULT_PAGE_SIZE;
  const next = offset + size;
  return HttpResponse.json({
    items: items.slice(offset, next),
    next_cursor: next < items.length ? `offset:${String(next)}` : "",
  });
}

function rolesPage(url: URL) {
  return pagedItems(url, rolesQuerySchema, mockRoles);
}

// Media servers the mock server starts with, ordered by name as the server
// orders them. Registration and removal change the working copy only.
export const UNREACHABLE_MEDIA_SERVER_HOST = "unreachable.example";
export const BUSY_MEDIA_SERVER_HOST = "busy.example";
export const mockServerInfo = {
  name: "Mock Jellyfin",
  version: "10.10.7",
  id: "mock-server-id",
} as const;

export const mockMediaServers: readonly MediaServer[] = [
  {
    id: "3d7f1a2b-0000-4000-8000-000000000001",
    kind: "jellyfin",
    name: "Cabin",
    base_url: "http://10.0.0.5:8096",
    allow_insecure: true,
    created_at: "2026-09-12T10:00:00Z",
    updated_at: "2026-09-12T10:00:00Z",
    capabilities: {
      create_user_with_password: true,
      set_password: true,
      quick_connect_approval: false,
      provider_id_lookup: false,
    },
  },
  {
    id: "3d7f1a2b-0000-4000-8000-000000000002",
    kind: "jellyfin",
    name: "Living room",
    base_url: "https://jellyfin.example",
    allow_insecure: false,
    created_at: "2026-09-01T08:00:00Z",
    updated_at: "2026-09-01T08:00:00Z",
    capabilities: {
      create_user_with_password: true,
      set_password: true,
      quick_connect_approval: true,
      provider_id_lookup: true,
    },
  },
];

let mediaServers: MediaServer[] = [...mockMediaServers];
let registeredCount = 0;

export function resetMockMediaServers(): void {
  mediaServers = [...mockMediaServers];
  registeredCount = 0;
}

function transportMismatch(input: RegisterMediaServerRequest): boolean {
  return input.base_url.startsWith("http://") !== input.allow_insecure;
}

function registerMockMediaServer(
  input: RegisterMediaServerRequest,
): MediaServer {
  registeredCount += 1;
  const ordinal = String(registeredCount).padStart(2, "0");
  const server: MediaServer = {
    id: `3d7f1a2b-0000-4000-8000-0000000001${ordinal}`,
    kind: input.kind,
    name: input.name,
    base_url: input.base_url.replace(/\/+$/u, ""),
    allow_insecure: input.allow_insecure,
    created_at: "2026-09-23T12:00:00Z",
    updated_at: "2026-09-23T12:00:00Z",
    capabilities: {
      create_user_with_password: true,
      set_password: true,
      quick_connect_approval: true,
      provider_id_lookup: false,
    },
  };
  mediaServers = [...mediaServers, server].sort((a, b) =>
    a.name.localeCompare(b.name, "en"),
  );
  return server;
}

function mediaServerDenial() {
  if (!signedIn) {
    return envelope(401, "unauthorized", "sign in required");
  }
  if (!granted.includes("admin.settings")) {
    return envelope(403, "forbidden", "missing permission admin.settings");
  }
  return undefined;
}

async function registerMediaServer(request: Request) {
  if (!sendsJson(request)) {
    return envelope(415, "unsupported_media_type", "expected JSON");
  }
  const input = registerMediaServerRequestSchema.safeParse(
    await request.json(),
  );
  if (!input.success || transportMismatch(input.data)) {
    return envelope(422, "validation_failed", "invalid media server");
  }
  const host = new URL(input.data.base_url).hostname;
  const name = input.data.name.toLowerCase();
  if (mediaServers.some((server) => server.name.toLowerCase() === name)) {
    return envelope(409, "already_exists", "a media server uses that name");
  }
  if (host === UNREACHABLE_MEDIA_SERVER_HOST) {
    return envelope(502, "media_server_failure", "media server probe failed", {
      "Retry-After": "5",
    });
  }
  if (host === BUSY_MEDIA_SERVER_HOST) {
    return envelope(503, "unavailable", "media server busy", {
      "Retry-After": "2",
    });
  }
  const server = registerMockMediaServer(input.data);
  return HttpResponse.json({ server, info: mockServerInfo }, { status: 201 });
}

function removeMediaServer(id: string | readonly string[] | undefined) {
  if (typeof id !== "string" || !z.uuid().safeParse(id).success) {
    return envelope(422, "validation_failed", "invalid id");
  }
  const index = mediaServers.findIndex((server) => server.id === id);
  if (index === -1) {
    return envelope(404, "not_found", "media server not found");
  }
  mediaServers = mediaServers.filter((server) => server.id !== id);
  return new HttpResponse(null, { status: 204 });
}

// Invites the mock server starts with, newest first as the server orders
// them. Codes are plaintext here only so tests can follow a link; the real
// server keeps digests.
const MAX_INVITE_CURSOR_LENGTH = 128;
const INVITE_RETRY_AFTER_SECONDS = 30;
export const VALID_INVITE_CODE = "ABCDEFGHIJKLMNOPQRSTUVWXYZ";
export const REVOKED_INVITE_CODE = "ABCDEFGHIJKLMNOPQRSTUVWXY2";
export const RATE_LIMITED_INVITE_CODE = "ABCDEFGHIJKLMNOPQRSTUVWXY3";
export const TAKEN_INVITE_USERNAME = "taken";
export const mockInviteRules = {
  username_rule:
    "1 to 64 characters: letters, digits, spaces, and - _ . ' @ + are allowed.",
  password_rule:
    "15 to 1024 characters. Common passwords and passwords used before are refused.",
} as const;

export const mockInvites: readonly Invite[] = [
  {
    id: "6a1b2c3d-0000-4000-8000-000000000001",
    media_server_id: "3d7f1a2b-0000-4000-8000-000000000001",
    label: "Family",
    expires_at: "2026-12-31T00:00:00Z",
    max_uses: 5,
    use_count: 1,
    library_ids: [],
    status: "active",
    created_at: "2026-09-20T09:00:00Z",
  },
  {
    id: "6a1b2c3d-0000-4000-8000-000000000002",
    media_server_id: "3d7f1a2b-0000-4000-8000-000000000002",
    label: "Old link",
    use_count: 2,
    library_ids: [],
    status: "revoked",
    created_at: "2026-09-01T09:00:00Z",
  },
];

let invites: Invite[] = [...mockInvites];
let inviteIdsByCode = new Map<string, string>();
let createdInvites = 0;

export function resetMockInvites(): void {
  invites = [...mockInvites];
  inviteIdsByCode = new Map([
    [VALID_INVITE_CODE, "6a1b2c3d-0000-4000-8000-000000000001"],
    [REVOKED_INVITE_CODE, "6a1b2c3d-0000-4000-8000-000000000002"],
  ]);
  createdInvites = 0;
}
resetMockInvites();

const invitesQuerySchema = pageQuerySchema(MAX_INVITE_CURSOR_LENGTH);

function inviteDenial() {
  if (!signedIn) {
    return envelope(401, "unauthorized", "sign in required");
  }
  if (!granted.includes("users.invite")) {
    return envelope(403, "forbidden", "missing permission users.invite");
  }
  return undefined;
}

function newestFirst(items: readonly Invite[]): Invite[] {
  return [...items].sort((a, b) => b.created_at.localeCompare(a.created_at));
}

function mediaServerById(id: string): MediaServer | undefined {
  return mediaServers.find((server) => server.id === id);
}

// The nth created invite gets a fixed, valid code so tests can follow it.
function nextInviteCode(): string {
  return `ABCDEFGHIJKLMNOPQRSTUVWXA${String.fromCharCode(65 + createdInvites)}`;
}

function createMockInvite(input: CreateInviteRequest): Invite {
  createdInvites += 1;
  const ordinal = String(createdInvites).padStart(2, "0");
  const invite: Invite = {
    id: `6a1b2c3d-0000-4000-8000-0000000001${ordinal}`,
    media_server_id: input.media_server_id,
    label: input.label,
    use_count: 0,
    library_ids: input.library_ids ?? [],
    status: "active",
    created_at: `2026-09-24T12:${ordinal}:00Z`,
  };
  if (input.expires_at !== undefined) {
    invite.expires_at = input.expires_at;
  }
  if (input.max_uses !== undefined) {
    invite.max_uses = input.max_uses;
  }
  invites = [invite, ...invites];
  return invite;
}

function upstreamFailure(server: MediaServer) {
  const host = new URL(server.base_url).hostname;
  if (host === UNREACHABLE_MEDIA_SERVER_HOST) {
    return envelope(502, "media_server_failure", "media server failed", {
      "Retry-After": "5",
    });
  }
  if (host === BUSY_MEDIA_SERVER_HOST) {
    return envelope(503, "unavailable", "media server busy", {
      "Retry-After": "2",
    });
  }
  return undefined;
}

async function createInvite(request: Request) {
  if (!sendsJson(request)) {
    return envelope(415, "unsupported_media_type", "expected JSON");
  }
  const input = createInviteRequestSchema.safeParse(await request.json());
  if (!input.success) {
    return envelope(422, "validation_failed", "invalid invite");
  }
  const server = mediaServerById(input.data.media_server_id);
  if (server === undefined) {
    return envelope(404, "not_found", "media server not found");
  }
  const failure = upstreamFailure(server);
  if (failure !== undefined) {
    return failure;
  }
  const code = nextInviteCode();
  const invite = createMockInvite(input.data);
  inviteIdsByCode.set(code, invite.id);
  return HttpResponse.json(
    { invite, code, accept_path: `/invite/${code}` },
    { status: 201 },
  );
}

function inviteById(id: string | readonly string[] | undefined) {
  if (typeof id !== "string" || !z.uuid().safeParse(id).success) {
    return { response: envelope(422, "validation_failed", "invalid id") };
  }
  const invite = invites.find((candidate) => candidate.id === id);
  if (invite === undefined) {
    return { response: envelope(404, "not_found", "invite not found") };
  }
  return { invite };
}

function revokeInvite(id: string | readonly string[] | undefined) {
  const found = inviteById(id);
  if (found.response !== undefined) {
    return found.response;
  }
  invites = invites.map((invite) =>
    invite.id === found.invite.id ? { ...invite, status: "revoked" } : invite,
  );
  return new HttpResponse(null, { status: 204 });
}

// The public answer for a code: the active invite it names, or one opaque
// 404 for a code that is malformed, unknown, expired, used up, or revoked.
function activeInviteForCode(code: string | readonly string[] | undefined) {
  if (typeof code !== "string" || !INVITE_CODE_PATTERN.test(code)) {
    return { response: envelope(404, "not_found", "invite not available") };
  }
  if (code === RATE_LIMITED_INVITE_CODE) {
    return {
      response: envelope(429, "rate_limited", "too many attempts", {
        "Retry-After": String(INVITE_RETRY_AFTER_SECONDS),
      }),
    };
  }
  const id = inviteIdsByCode.get(code);
  const invite = invites.find((candidate) => candidate.id === id);
  const server =
    invite === undefined ? undefined : mediaServerById(invite.media_server_id);
  if (invite?.status !== "active" || server === undefined) {
    return { response: envelope(404, "not_found", "invite not available") };
  }
  return { invite, server };
}

function previewInvite(code: string | readonly string[] | undefined) {
  const found = activeInviteForCode(code);
  if (found.response !== undefined) {
    return found.response;
  }
  return HttpResponse.json({
    media_server_name: found.server.name,
    ...mockInviteRules,
  });
}

async function acceptInvite(
  code: string | readonly string[] | undefined,
  request: Request,
) {
  const found = activeInviteForCode(code);
  if (found.response !== undefined) {
    return found.response;
  }
  if (!sendsJson(request)) {
    return envelope(415, "unsupported_media_type", "expected JSON");
  }
  const input = acceptInviteRequestSchema.safeParse(await request.json());
  if (!input.success) {
    return envelope(422, "validation_failed", "invalid account details");
  }
  if (input.data.username.toLowerCase() === TAKEN_INVITE_USERNAME) {
    return envelope(409, "username_unavailable", "username taken");
  }
  const failure = upstreamFailure(found.server);
  if (failure !== undefined) {
    return failure;
  }
  const useCount = found.invite.use_count + 1;
  const exhausted =
    found.invite.max_uses !== undefined && useCount >= found.invite.max_uses;
  invites = invites.map((invite) =>
    invite.id === found.invite.id
      ? {
          ...invite,
          use_count: useCount,
          status: exhausted ? "exhausted" : "active",
        }
      : invite,
  );
  return HttpResponse.json(
    { media_server_name: found.server.name, username: input.data.username },
    { status: 201 },
  );
}

export const handlers = [
  http.get(
    "*/api/v1/invites",
    jsonApi(
      ({ request }) =>
        inviteDenial() ??
        pagedItems(
          new URL(request.url),
          invitesQuerySchema,
          newestFirst(invites),
        ),
    ),
  ),
  http.post(
    "*/api/v1/invites",
    jsonApi(
      async ({ request }) => inviteDenial() ?? (await createInvite(request)),
    ),
  ),
  http.get(
    "*/api/v1/invites/:id",
    jsonApi(({ params }) => {
      const denied = inviteDenial();
      if (denied !== undefined) {
        return denied;
      }
      const found = inviteById(params.id);
      return found.response ?? HttpResponse.json(found.invite);
    }),
  ),
  http.delete(
    "*/api/v1/invites/:id",
    jsonApi(({ params }) => inviteDenial() ?? revokeInvite(params.id)),
  ),
  http.get(
    "*/api/v1/invite/:code",
    jsonApi(({ params }) => previewInvite(params.code)),
  ),
  http.post(
    "*/api/v1/invite/:code/accept",
    jsonApi(({ params, request }) => acceptInvite(params.code, request)),
  ),
  http.get(
    "*/api/v1/media-servers",
    jsonApi(
      ({ request }) =>
        mediaServerDenial() ??
        pagedItems(new URL(request.url), mediaServersQuerySchema, mediaServers),
    ),
  ),
  http.post(
    "*/api/v1/media-servers",
    jsonApi(
      async ({ request }) =>
        mediaServerDenial() ?? (await registerMediaServer(request)),
    ),
  ),
  http.delete(
    "*/api/v1/media-servers/:id",
    jsonApi(
      ({ params }) => mediaServerDenial() ?? removeMediaServer(params.id),
    ),
  ),
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
    "*/api/v1/auth/providers",
    jsonApi(() => HttpResponse.json({ providers })),
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
