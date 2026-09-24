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
import type {
  HistoryWatch,
  Watch,
} from "../features/playback/api/playback-schemas.js";
import {
  registerDownloadManagerRequestSchema,
  type DownloadManager,
  type DownloadManagerOptions,
} from "../features/requests/api/download-manager-schemas.js";
import {
  createMediaRequestSchema,
  type MetadataSeries,
  type MetadataTitle,
} from "../features/requests/api/metadata-schemas.js";
import {
  mediaKindSchema,
  metadataKeyRequestSchema,
  requestDecisionSchema,
  requestProfileInputSchema,
  requestStatusSchema,
  type MediaRequest,
  type RequestProfile,
} from "../features/requests/api/requests-schemas.js";
import {
  requestQuotaInputSchema,
  type RequestQuota,
} from "../features/roles/api/quota-schemas.js";
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
  "metadata_not_configured",
  "metadata_provider_failure",
  "request_quota_exceeded",
  "request_profile_in_use",
  "invalid_request_transition",
  "download_manager_failure",
  "download_manager_not_found",
  "download_manager_in_use",
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

// Role request quotas: custom-01 starts with one, nothing else does.
export const QUOTA_ROLE_ID = "8e1c2f7a-0000-4000-8000-000000000101";
const mockRoleQuotas: readonly RequestQuota[] = [
  {
    scope_id: QUOTA_ROLE_ID,
    movie_limit: 2,
    movie_period_days: 30,
    season_limit: 4,
    season_period_days: 30,
  },
];
let roleQuotas = new Map(
  mockRoleQuotas.map((quota) => [quota.scope_id, quota]),
);

export function resetMockRoleQuotas(): void {
  roleQuotas = new Map(mockRoleQuotas.map((quota) => [quota.scope_id, quota]));
}

function roleQuotaDenial(id: string | readonly string[] | undefined) {
  if (!signedIn) {
    return envelope(401, "unauthorized", "sign in required");
  }
  if (!granted.includes("admin.roles")) {
    return envelope(403, "forbidden", "missing permission admin.roles");
  }
  if (typeof id !== "string" || !z.uuid().safeParse(id).success) {
    return envelope(404, "not_found", "role not found");
  }
  if (!mockRoles.some((role) => role.id === id)) {
    return envelope(404, "not_found", "role not found");
  }
  return undefined;
}

async function setRoleQuota(id: string, request: Request) {
  if (!sendsJson(request)) {
    return envelope(415, "unsupported_media_type", "expected JSON");
  }
  const input = requestQuotaInputSchema.safeParse(await request.json());
  if (!input.success) {
    return envelope(422, "validation_failed", "invalid quota");
  }
  const quota: RequestQuota = { scope_id: id, ...input.data };
  roleQuotas.set(id, quota);
  return HttpResponse.json(quota);
}

const roleQuotaHandlers = [
  http.get(
    "*/api/v1/roles/:id/request-quota",
    jsonApi(({ params }) => {
      const denied = roleQuotaDenial(params.id);
      if (denied !== undefined) {
        return denied;
      }
      const quota = roleQuotas.get(String(params.id));
      return quota === undefined
        ? envelope(404, "not_found", "no quota")
        : HttpResponse.json(quota);
    }),
  ),
  http.put(
    "*/api/v1/roles/:id/request-quota",
    jsonApi(
      async ({ params, request }) =>
        roleQuotaDenial(params.id) ??
        (await setRoleQuota(String(params.id), request)),
    ),
  ),
  http.delete(
    "*/api/v1/roles/:id/request-quota",
    jsonApi(({ params }) => {
      const denied = roleQuotaDenial(params.id);
      if (denied !== undefined) {
        return denied;
      }
      if (!roleQuotas.delete(String(params.id))) {
        return envelope(404, "not_found", "no quota");
      }
      return new HttpResponse(null, { status: 204 });
    }),
  ),
];

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

// Watches the mock server reports: two playing now and a short history.
const MAX_PLAYBACK_CURSOR_LENGTH = 256;
const CABIN = "3d7f1a2b-0000-4000-8000-000000000001";
const LIVING_ROOM = "3d7f1a2b-0000-4000-8000-000000000002";

function watch(patch: Partial<Watch> & Pick<Watch, "id">): Watch {
  return {
    media_server_id: CABIN,
    media_server_name: "Cabin",
    media_user_id: "u-alice",
    username: "alice",
    device_id: "d-tv",
    device_name: "Living room TV",
    client: "Jellyfin Web",
    item_id: "i-1",
    item_name: "Pilot",
    item_type: "Episode",
    series_name: "Fringe",
    season_number: 1,
    episode_number: 1,
    position_ms: 754_000,
    paused: false,
    play_method: "direct_play",
    active_seconds: 754,
    started_at: "2026-09-24T19:00:00Z",
    ...patch,
  };
}

export const mockNowPlaying: readonly Watch[] = [
  watch({ id: "7b2c3d4e-0000-4000-8000-000000000001" }),
  watch({
    id: "7b2c3d4e-0000-4000-8000-000000000002",
    media_server_id: LIVING_ROOM,
    media_server_name: "Living room",
    media_user_id: "u-bob",
    username: "bob",
    device_id: "d-phone",
    device_name: "Pixel",
    client: "Findroid",
    item_id: "i-2",
    item_name: "Heat",
    item_type: "Movie",
    series_name: "",
    season_number: null,
    episode_number: null,
    position_ms: 5_400_000,
    paused: true,
    play_method: "transcode",
    active_seconds: 3_610,
    started_at: "2026-09-24T18:00:00Z",
  }),
];

export const mockPlaybackHistory: readonly HistoryWatch[] = [
  {
    ...watch({
      id: "7b2c3d4e-0000-4000-8000-000000000011",
      item_id: "i-3",
      item_name: "The Arrival",
      season_number: 1,
      episode_number: 4,
      position_ms: 2_640_000,
      active_seconds: 2_580,
      started_at: "2026-09-23T21:00:00Z",
    }),
    ended_at: "2026-09-23T21:44:00Z",
  },
  {
    ...watch({
      id: "7b2c3d4e-0000-4000-8000-000000000012",
      media_server_id: LIVING_ROOM,
      media_server_name: "Living room",
      media_user_id: "u-bob",
      username: "bob",
      device_id: "d-phone",
      device_name: "Pixel",
      client: "Findroid",
      item_id: "i-4",
      item_name: "Ronin",
      item_type: "Movie",
      series_name: "",
      season_number: null,
      episode_number: null,
      position_ms: 7_300_000,
      play_method: "direct_stream",
      active_seconds: 7_250,
      started_at: "2026-09-22T20:00:00Z",
    }),
    ended_at: "2026-09-22T22:02:00Z",
  },
];

const playbackHistoryQuerySchema = pageQuerySchema(MAX_PLAYBACK_CURSOR_LENGTH);

function playbackDenial() {
  if (!signedIn) {
    return envelope(401, "unauthorized", "sign in required");
  }
  if (!granted.includes("stats.read.all")) {
    return envelope(403, "forbidden", "missing permission stats.read.all");
  }
  return undefined;
}

function playbackHistory(url: URL) {
  const serverIds = url.searchParams.getAll("media_server_id");
  if (serverIds.length > 1) {
    return envelope(422, "validation_failed", "repeated media_server_id");
  }
  const serverId = serverIds[0];
  if (serverId !== undefined && !z.uuid().safeParse(serverId).success) {
    return envelope(422, "validation_failed", "invalid media_server_id");
  }
  const items =
    serverId === undefined
      ? mockPlaybackHistory
      : mockPlaybackHistory.filter((w) => w.media_server_id === serverId);
  return pagedItems(url, playbackHistoryQuerySchema, items);
}

// Requests: profiles, the TMDB key's presence, and the requests themselves.
// Decisions change the working copy only; the key is never kept, only the
// fact that one was stored.
const MAX_PROFILE_CURSOR_LENGTH = 100;
const MAX_REQUEST_CURSOR_LENGTH = 140;
export const REQUESTER_ALICE = "0b6c3d2e-1111-4a2b-9c3d-000000000002";
export const REQUESTER_BOB = "0b6c3d2e-1111-4a2b-9c3d-000000000003";
export const PROFILE_MOVIES = "9c1d2e3f-0000-4000-8000-000000000001";
export const PROFILE_SERIES = "9c1d2e3f-0000-4000-8000-000000000002";
export const INVALID_TMDB_KEY = "rejected-key";
export const PROCESSING_REQUEST_ID = "5e4d3c2b-0000-4000-8000-000000000005";
export const FAILED_REQUEST_ID = "5e4d3c2b-0000-4000-8000-000000000006";

export const mockRequestProfiles: readonly RequestProfile[] = [
  {
    id: PROFILE_MOVIES,
    name: "Movies HD",
    kinds: ["movie"],
    download_manager_kind: "radarr",
    download_manager_instance: "radarr-main",
    quality_profile: "HD-1080p",
    root_folder: "/data/movies",
    tags: ["bloom"],
    created_at: "2026-09-15T10:00:00Z",
    updated_at: "2026-09-15T10:00:00Z",
  },
  {
    id: PROFILE_SERIES,
    name: "Series",
    kinds: ["series"],
    download_manager_kind: "sonarr",
    download_manager_instance: "sonarr-main",
    quality_profile: "Any",
    root_folder: "/data/tv",
    tags: [],
    created_at: "2026-09-16T10:00:00Z",
    updated_at: "2026-09-16T10:00:00Z",
  },
];

function mediaRequest(overrides: Partial<MediaRequest>): MediaRequest {
  return {
    id: "5e4d3c2b-0000-4000-8000-000000000000",
    kind: "movie",
    provider: "tmdb",
    provider_id: "949",
    title: "Heat",
    year: 1995,
    poster_path: "/heat.jpg",
    requester_account_id: REQUESTER_ALICE,
    profile_id: PROFILE_MOVIES,
    status: "pending",
    seasons: [],
    decision_reason: "",
    failure_reason: "",
    download_manager_id: "",
    download_manager_item_id: "",
    dispatch_quality_profile: "",
    dispatch_root_folder: "",
    dispatch_tags: [],
    decided_by_account_id: "",
    created_at: "2026-09-22T09:00:00Z",
    updated_at: "2026-09-22T09:00:00Z",
    ...overrides,
  };
}

// Newest first, as the server orders them.
export const mockRequests: readonly MediaRequest[] = [
  mediaRequest({
    id: "5e4d3c2b-0000-4000-8000-000000000001",
    kind: "series",
    provider_id: "1396",
    title: "The Arrival",
    year: 2021,
    poster_path: "/arrival.jpg",
    requester_account_id: REQUESTER_BOB,
    profile_id: PROFILE_SERIES,
    seasons: [
      { number: 1, status: "pending" },
      { number: 2, status: "pending" },
    ],
    created_at: "2026-09-23T08:00:00Z",
    updated_at: "2026-09-23T08:00:00Z",
  }),
  mediaRequest({ id: "5e4d3c2b-0000-4000-8000-000000000002" }),
  mediaRequest({
    id: "5e4d3c2b-0000-4000-8000-000000000003",
    provider_id: "1124",
    title: "The Prestige",
    year: 2006,
    poster_path: "",
    requester_account_id: mockAccount.id,
    status: "declined",
    decision_reason: "Already on the shelf.",
    decided_by_account_id: mockAccount.id,
    decided_at: "2026-09-21T12:00:00Z",
    created_at: "2026-09-20T09:00:00Z",
    updated_at: "2026-09-21T12:00:00Z",
  }),
  mediaRequest({
    id: PROCESSING_REQUEST_ID,
    provider_id: "603",
    title: "The Matrix",
    year: 1999,
    poster_path: "",
    requester_account_id: mockAccount.id,
    status: "processing",
    download_manager_id: "7d8e9f0a-0000-4000-8000-000000000001",
    download_manager_item_id: "radarr:42",
    dispatch_quality_profile: "HD-1080p",
    dispatch_root_folder: "/data/movies",
    dispatch_tags: ["bloom"],
    decided_by_account_id: mockAccount.id,
    decided_at: "2026-09-21T15:00:00Z",
    created_at: "2026-09-21T14:00:00Z",
    updated_at: "2026-09-21T15:00:00Z",
  }),
  mediaRequest({
    id: FAILED_REQUEST_ID,
    provider_id: "680",
    title: "Pulp Fiction",
    year: 1994,
    poster_path: "",
    requester_account_id: REQUESTER_BOB,
    status: "failed",
    failure_reason: "radarr-main refused the request: root folder missing.",
    decided_by_account_id: mockAccount.id,
    decided_at: "2026-09-21T13:00:00Z",
    created_at: "2026-09-21T12:00:00Z",
    updated_at: "2026-09-21T13:30:00Z",
  }),
  mediaRequest({
    id: "5e4d3c2b-0000-4000-8000-000000000004",
    provider_id: "27205",
    title: "Inception",
    year: 2010,
    requester_account_id: mockAccount.id,
    status: "available",
    decided_by_account_id: mockAccount.id,
    decided_at: "2026-09-19T12:00:00Z",
    created_at: "2026-09-18T09:00:00Z",
    updated_at: "2026-09-19T13:00:00Z",
  }),
];

let requestProfiles: RequestProfile[] = [...mockRequestProfiles];
let requests: MediaRequest[] = [...mockRequests];
let tmdbKeyConfigured = false;
let createdProfiles = 0;

export function resetMockRequests(): void {
  resetMockDownloadManagers();
  requestProfiles = [...mockRequestProfiles];
  requests = [...mockRequests];
  tmdbKeyConfigured = false;
  createdProfiles = 0;
  createdRequests = 0;
}

export function setMockTmdbKeyConfigured(configured: boolean): void {
  tmdbKeyConfigured = configured;
}

const profilesQuerySchema = pageQuerySchema(MAX_PROFILE_CURSOR_LENGTH);
const requestsQuerySchema = pageQuerySchema(MAX_REQUEST_CURSOR_LENGTH);

function permissionDenial(permission: KnownPermission) {
  if (!signedIn) {
    return envelope(401, "unauthorized", "sign in required");
  }
  if (!granted.includes(permission)) {
    return envelope(403, "forbidden", `missing permission ${permission}`);
  }
  return undefined;
}

function anyPermissionDenial(needed: readonly KnownPermission[]) {
  if (!signedIn) {
    return envelope(401, "unauthorized", "sign in required");
  }
  if (!needed.some((permission) => granted.includes(permission))) {
    return envelope(
      403,
      "forbidden",
      `missing permission ${needed.join(" or ")}`,
    );
  }
  return undefined;
}

function byName(a: RequestProfile, b: RequestProfile): number {
  return a.name.localeCompare(b.name, "en");
}

async function saveProfile(request: Request, id: string | undefined) {
  if (!sendsJson(request)) {
    return envelope(415, "unsupported_media_type", "expected JSON");
  }
  const input = requestProfileInputSchema.safeParse(await request.json());
  if (!input.success) {
    return envelope(422, "validation_failed", "invalid profile");
  }
  const existing = requestProfiles.find((profile) => profile.id === id);
  if (id !== undefined && existing === undefined) {
    return envelope(404, "not_found", "profile not found");
  }
  const name = input.data.name.toLowerCase();
  if (
    requestProfiles.some(
      (profile) => profile.id !== id && profile.name.toLowerCase() === name,
    )
  ) {
    return envelope(409, "already_exists", "a profile uses that name");
  }
  createdProfiles += existing === undefined ? 1 : 0;
  const ordinal = String(createdProfiles).padStart(2, "0");
  const profile: RequestProfile = {
    id: existing?.id ?? `9c1d2e3f-0000-4000-8000-0000000001${ordinal}`,
    ...input.data,
    created_at: existing?.created_at ?? "2026-09-23T12:00:00Z",
    updated_at: "2026-09-23T12:00:00Z",
  };
  requestProfiles = [
    ...requestProfiles.filter((candidate) => candidate.id !== profile.id),
    profile,
  ].sort(byName);
  return HttpResponse.json(profile, {
    status: existing === undefined ? 201 : 200,
  });
}

function removeProfile(id: string | readonly string[] | undefined) {
  if (typeof id !== "string" || !z.uuid().safeParse(id).success) {
    return envelope(422, "validation_failed", "invalid id");
  }
  if (!requestProfiles.some((profile) => profile.id === id)) {
    return envelope(404, "not_found", "profile not found");
  }
  if (requests.some((request) => request.profile_id === id)) {
    return envelope(409, "already_exists", "requests reference this profile");
  }
  requestProfiles = requestProfiles.filter((profile) => profile.id !== id);
  return new HttpResponse(null, { status: 204 });
}

function listRequests(url: URL) {
  if (!granted.includes("requests.approve")) {
    // A reader without requests.approve only ever sees their own.
    url.searchParams.set("requester_id", mockAccount.id);
  }
  const statuses = url.searchParams.getAll("status");
  const requesters = url.searchParams.getAll("requester_id");
  if (statuses.length > 1 || requesters.length > 1) {
    return envelope(422, "validation_failed", "repeated filter");
  }
  const status = requestStatusSchema.safeParse(statuses[0]);
  if (statuses[0] !== undefined && !status.success) {
    return envelope(422, "validation_failed", "invalid status");
  }
  if (
    requesters[0] !== undefined &&
    !z.uuid().safeParse(requesters[0]).success
  ) {
    return envelope(422, "validation_failed", "invalid requester_id");
  }
  const items = requests.filter(
    (request) =>
      (statuses[0] === undefined || request.status === statuses[0]) &&
      (requesters[0] === undefined ||
        request.requester_account_id === requesters[0]),
  );
  return pagedItems(url, requestsQuerySchema, items);
}

async function decideRequest(
  id: string | readonly string[] | undefined,
  verb: "approve" | "decline",
  request: Request,
) {
  if (typeof id !== "string" || !z.uuid().safeParse(id).success) {
    return envelope(422, "validation_failed", "invalid id");
  }
  if (!sendsJson(request)) {
    return envelope(415, "unsupported_media_type", "expected JSON");
  }
  const input = requestDecisionSchema.safeParse(await request.json());
  if (!input.success) {
    return envelope(422, "validation_failed", "invalid decision");
  }
  const existing = requests.find((candidate) => candidate.id === id);
  if (existing === undefined) {
    return envelope(404, "not_found", "request not found");
  }
  const reapproval = existing.status === "failed" && verb === "approve";
  if (existing.status !== "pending" && !reapproval) {
    return envelope(
      409,
      "invalid_request_transition",
      "request already decided",
    );
  }
  const status = verb === "approve" ? "approved" : "declined";
  const decided: MediaRequest = {
    ...existing,
    status,
    seasons: existing.seasons.map((season) => ({ ...season, status })),
    decision_reason: input.data.reason ?? "",
    failure_reason: "",
    download_manager_item_id: reapproval
      ? existing.download_manager_item_id
      : "",
    decided_by_account_id: mockAccount.id,
    decided_at: "2026-09-23T12:00:00Z",
    updated_at: "2026-09-23T12:00:00Z",
  };
  requests = requests.map((candidate) =>
    candidate.id === id ? decided : candidate,
  );
  return HttpResponse.json(decided);
}

async function storeTmdbKey(request: Request) {
  if (!sendsJson(request)) {
    return envelope(415, "unsupported_media_type", "expected JSON");
  }
  const input = metadataKeyRequestSchema.safeParse(await request.json());
  if (!input.success || input.data.api_key === INVALID_TMDB_KEY) {
    return envelope(422, "validation_failed", "invalid api key");
  }
  tmdbKeyConfigured = true;
  return HttpResponse.json({ configured: true });
}

function removeTmdbKey() {
  if (!tmdbKeyConfigured) {
    return envelope(404, "not_found", "no key stored");
  }
  tmdbKeyConfigured = false;
  return new HttpResponse(null, { status: 204 });
}

// The metadata provider's catalogue as the mock knows it. Searching matches
// on the title; "unconfigured" and "broken" exercise the two failure modes.
export const UNCONFIGURED_QUERY = "unconfigured";
export const BROKEN_QUERY = "broken";
export const QUOTA_MOVIE_ID = "550";
export const MISSING_TITLE_ID = "99999";
const MOCK_MOVIE_ID = "438631";

export const mockCatalogue: readonly MetadataTitle[] = [
  {
    kind: "movie",
    provider: "tmdb",
    provider_id: "949",
    title: "Heat",
    year: 1995,
    overview: "A group of professional bank robbers start to feel the heat.",
    poster_path: "/heat.jpg",
  },
  {
    kind: "movie",
    provider: "tmdb",
    provider_id: MOCK_MOVIE_ID,
    title: "Dune",
    year: 2021,
    overview:
      "Paul Atreides travels to the most dangerous planet in the universe.",
    poster_path: "",
  },
  {
    kind: "movie",
    provider: "tmdb",
    provider_id: QUOTA_MOVIE_ID,
    title: "Fight Club",
    year: 1999,
    overview: "An insomniac office worker forms an underground fight club.",
    poster_path: "/fight.jpg",
  },
  {
    kind: "series",
    provider: "tmdb",
    provider_id: "1396",
    title: "The Arrival",
    year: 2021,
    overview: "Strangers land in a small town and nothing is the same again.",
    poster_path: "/arrival.jpg",
  },
];

const mockSeasons: readonly MetadataSeries["seasons"][number][] = [
  { number: 0, name: "Specials", episode_count: 2 },
  {
    number: 1,
    name: "Season 1",
    episode_count: 8,
    air_date: "2021-03-01T00:00:00Z",
  },
  {
    number: 2,
    name: "Season 2",
    episode_count: 8,
    air_date: "2022-03-01T00:00:00Z",
  },
  {
    number: 3,
    name: "Season 3",
    episode_count: 10,
    air_date: "2023-03-01T00:00:00Z",
  },
];

function searchTitles(url: URL) {
  const q = url.searchParams.get("q") ?? "";
  const kind = url.searchParams.get("kind");
  if (
    q.trim() === "" ||
    q.length > 200 ||
    url.searchParams.getAll("q").length > 1
  ) {
    return envelope(422, "validation_failed", "invalid query");
  }
  if (kind !== null && !mediaKindSchema.safeParse(kind).success) {
    return envelope(422, "validation_failed", "invalid kind");
  }
  if (q === UNCONFIGURED_QUERY) {
    return envelope(503, "metadata_not_configured", "no TMDB key");
  }
  if (q === BROKEN_QUERY) {
    return envelope(502, "metadata_provider_failure", "provider failed");
  }
  const needle = q.toLowerCase();
  return HttpResponse.json({
    items: mockCatalogue.filter(
      (title) =>
        title.title.toLowerCase().includes(needle) &&
        (kind === null || title.kind === kind),
    ),
  });
}

function titleDetail(
  kind: "movie" | "series",
  id: string | readonly string[] | undefined,
) {
  if (typeof id !== "string" || !/^[1-9][0-9]{0,19}$/.test(id)) {
    return envelope(422, "validation_failed", "invalid id");
  }
  const title = mockCatalogue.find(
    (candidate) => candidate.kind === kind && candidate.provider_id === id,
  );
  if (title === undefined) {
    return envelope(404, "not_found", "title not found");
  }
  return HttpResponse.json(
    kind === "series" ? { ...title, seasons: mockSeasons } : title,
  );
}

let createdRequests = 0;

async function createRequest(request: Request) {
  if (!sendsJson(request)) {
    return envelope(415, "unsupported_media_type", "expected JSON");
  }
  const input = createMediaRequestSchema.safeParse(await request.json());
  if (!input.success) {
    return envelope(422, "validation_failed", "invalid request");
  }
  const title = mockCatalogue.find(
    (candidate) =>
      candidate.kind === input.data.kind &&
      candidate.provider_id === input.data.provider_id,
  );
  const profile = requestProfiles.find((p) => p.id === input.data.profile_id);
  if (title === undefined || profile === undefined) {
    return envelope(404, "not_found", "title or profile not found");
  }
  if (!profile.kinds.includes(input.data.kind)) {
    return envelope(422, "validation_failed", "profile does not accept kind");
  }
  if (input.data.provider_id === QUOTA_MOVIE_ID) {
    return envelope(422, "request_quota_exceeded", "quota exceeded");
  }
  const active = requests.filter(
    (existing) =>
      existing.provider_id === input.data.provider_id &&
      existing.kind === input.data.kind &&
      existing.status !== "declined" &&
      existing.status !== "failed",
  );
  const overlap = active.some(
    (existing) =>
      existing.kind === "movie" ||
      existing.seasons.some((season) =>
        input.data.seasons.includes(season.number),
      ),
  );
  if (overlap) {
    return envelope(409, "already_exists", "already requested");
  }
  createdRequests += 1;
  const status = granted.includes("requests.approve") ? "approved" : "pending";
  const created: MediaRequest = {
    id: `5e4d3c2b-0000-4000-8000-0000000002${String(createdRequests).padStart(2, "0")}`,
    kind: title.kind,
    provider: "tmdb",
    provider_id: title.provider_id,
    title: title.title,
    year: title.year,
    poster_path: title.poster_path,
    requester_account_id: mockAccount.id,
    profile_id: profile.id,
    status,
    seasons: input.data.seasons.map((number) => ({ number, status })),
    decision_reason: "",
    failure_reason: "",
    download_manager_id: "",
    download_manager_item_id: "",
    dispatch_quality_profile: "",
    dispatch_root_folder: "",
    dispatch_tags: [],
    decided_by_account_id: status === "approved" ? mockAccount.id : "",
    ...(status === "approved" ? { decided_at: "2026-09-23T12:00:00Z" } : {}),
    created_at: "2026-09-23T12:00:00Z",
    updated_at: "2026-09-23T12:00:00Z",
  };
  requests = [created, ...requests];
  return HttpResponse.json(created, { status: 201 });
}

// Download managers the mock server starts with, ordered by name. The
// hostname below exercises a failed probe.
export const UNREACHABLE_MANAGER_HOST = "unreachable.example";
export const MANAGER_RADARR_ID = "7d8e9f0a-0000-4000-8000-000000000001";
export const MANAGER_SONARR_ID = "7d8e9f0a-0000-4000-8000-000000000002";
const MAX_MANAGER_CURSOR_LENGTH = 400;

export const mockDownloadManagers: readonly DownloadManager[] = [
  {
    id: MANAGER_RADARR_ID,
    kind: "radarr",
    name: "radarr-main",
    base_url: "https://radarr.example",
    allow_insecure: false,
    created_at: "2026-09-14T10:00:00Z",
    updated_at: "2026-09-14T10:00:00Z",
  },
  {
    id: MANAGER_SONARR_ID,
    kind: "sonarr",
    name: "sonarr-main",
    base_url: "http://10.0.0.7:8989",
    allow_insecure: true,
    created_at: "2026-09-14T10:00:00Z",
    updated_at: "2026-09-14T10:00:00Z",
  },
];

const mockManagerOptions: Readonly<Record<string, DownloadManagerOptions>> = {
  [MANAGER_RADARR_ID]: {
    quality_profiles: [
      { id: "1", name: "HD-1080p" },
      { id: "2", name: "Ultra-HD" },
    ],
    root_folders: [
      { id: "/data/movies", name: "/data/movies" },
      { id: "/data/movies4k", name: "/data/movies4k" },
    ],
    tags: [
      { id: "1", name: "bloom" },
      { id: "2", name: "4k" },
    ],
  },
  [MANAGER_SONARR_ID]: {
    quality_profiles: [{ id: "1", name: "Any" }],
    root_folders: [{ id: "/data/tv", name: "/data/tv" }],
    tags: [],
  },
};

let downloadManagers: DownloadManager[] = [...mockDownloadManagers];
let registeredManagers = 0;

export function resetMockDownloadManagers(): void {
  downloadManagers = [...mockDownloadManagers];
  registeredManagers = 0;
}

const managersQuerySchema = pageQuerySchema(MAX_MANAGER_CURSOR_LENGTH);

async function registerManager(request: Request) {
  if (!sendsJson(request)) {
    return envelope(415, "unsupported_media_type", "expected JSON");
  }
  const input = registerDownloadManagerRequestSchema.safeParse(
    await request.json(),
  );
  if (
    !input.success ||
    input.data.base_url.startsWith("http://") !== input.data.allow_insecure
  ) {
    return envelope(422, "validation_failed", "invalid download manager");
  }
  const name = input.data.name.toLowerCase();
  if (downloadManagers.some((m) => m.name.toLowerCase() === name)) {
    return envelope(409, "already_exists", "a download manager uses that name");
  }
  if (new URL(input.data.base_url).hostname === UNREACHABLE_MANAGER_HOST) {
    return envelope(502, "download_manager_failure", "probe failed");
  }
  registeredManagers += 1;
  const ordinal = String(registeredManagers).padStart(2, "0");
  const manager: DownloadManager = {
    id: `7d8e9f0a-0000-4000-8000-0000000001${ordinal}`,
    kind: input.data.kind,
    name: input.data.name,
    base_url: input.data.base_url.replace(/\/+$/u, ""),
    allow_insecure: input.data.allow_insecure,
    created_at: "2026-09-23T12:00:00Z",
    updated_at: "2026-09-23T12:00:00Z",
  };
  downloadManagers = [...downloadManagers, manager].sort((a, b) =>
    a.name.localeCompare(b.name, "en"),
  );
  const kind = input.data.kind === "radarr" ? "movie" : "series";
  return HttpResponse.json(
    {
      manager,
      info: {
        name: input.data.kind === "radarr" ? "Radarr" : "Sonarr",
        version: "5.0.0",
        capabilities: { kinds: [kind] },
      },
    },
    { status: 201 },
  );
}

function removeManager(id: string | readonly string[] | undefined) {
  if (typeof id !== "string" || !z.uuid().safeParse(id).success) {
    return envelope(422, "validation_failed", "invalid id");
  }
  const manager = downloadManagers.find((m) => m.id === id);
  if (manager === undefined) {
    return envelope(404, "not_found", "download manager not found");
  }
  if (
    requestProfiles.some(
      (profile) => profile.download_manager_instance === manager.name,
    )
  ) {
    return envelope(409, "download_manager_in_use", "profiles reference it");
  }
  downloadManagers = downloadManagers.filter((m) => m.id !== id);
  return new HttpResponse(null, { status: 204 });
}

function managerOptions(id: string | readonly string[] | undefined) {
  if (typeof id !== "string" || !downloadManagers.some((m) => m.id === id)) {
    return envelope(404, "not_found", "download manager not found");
  }
  const options = mockManagerOptions[id];
  return HttpResponse.json(
    options ?? { quality_profiles: [], root_folders: [], tags: [] },
  );
}

function requestProgress(id: string | readonly string[] | undefined) {
  const request = requests.find((candidate) => candidate.id === id);
  if (request === undefined) {
    return envelope(404, "not_found", "request not found");
  }
  if (request.download_manager_item_id === "") {
    return envelope(404, "download_manager_not_found", "not dispatched");
  }
  return HttpResponse.json({
    status: "downloading",
    size: 4_000_000_000,
    size_left: 1_000_000_000,
    estimated_completion: "2026-09-23T13:30:00Z",
  });
}

const downloadManagerHandlers = [
  http.get(
    "*/api/v1/download-managers",
    jsonApi(
      ({ request }) =>
        permissionDenial("admin.settings") ??
        pagedItems(new URL(request.url), managersQuerySchema, downloadManagers),
    ),
  ),
  http.post(
    "*/api/v1/download-managers",
    jsonApi(
      async ({ request }) =>
        permissionDenial("admin.settings") ?? (await registerManager(request)),
    ),
  ),
  http.delete(
    "*/api/v1/download-managers/:id",
    jsonApi(
      ({ params }) =>
        permissionDenial("admin.settings") ?? removeManager(params.id),
    ),
  ),
  http.get(
    "*/api/v1/download-managers/:id/options",
    jsonApi(
      ({ params }) =>
        permissionDenial("admin.settings") ?? managerOptions(params.id),
    ),
  ),
  http.get(
    "*/api/v1/requests/:id/progress",
    jsonApi(
      ({ params }) =>
        anyPermissionDenial(["requests.read.own", "requests.approve"]) ??
        requestProgress(params.id),
    ),
  ),
];

const requestHandlers = [
  ...downloadManagerHandlers,
  http.get(
    "*/api/v1/request-profiles",
    jsonApi(
      ({ request }) =>
        anyPermissionDenial(["requests.create", "admin.settings"]) ??
        pagedItems(new URL(request.url), profilesQuerySchema, requestProfiles),
    ),
  ),
  http.post(
    "*/api/v1/request-profiles",
    jsonApi(
      async ({ request }) =>
        permissionDenial("admin.settings") ??
        (await saveProfile(request, undefined)),
    ),
  ),
  http.put(
    "*/api/v1/request-profiles/:id",
    jsonApi(async ({ params, request }) => {
      const denied = permissionDenial("admin.settings");
      if (denied !== undefined) {
        return denied;
      }
      if (
        typeof params.id !== "string" ||
        !z.uuid().safeParse(params.id).success
      ) {
        return envelope(422, "validation_failed", "invalid id");
      }
      return saveProfile(request, params.id);
    }),
  ),
  http.delete(
    "*/api/v1/request-profiles/:id",
    jsonApi(
      ({ params }) =>
        permissionDenial("admin.settings") ?? removeProfile(params.id),
    ),
  ),
  http.get(
    "*/api/v1/requests",
    jsonApi(
      ({ request }) =>
        anyPermissionDenial(["requests.read.own", "requests.approve"]) ??
        listRequests(new URL(request.url)),
    ),
  ),
  http.post(
    "*/api/v1/requests",
    jsonApi(
      async ({ request }) =>
        permissionDenial("requests.create") ?? (await createRequest(request)),
    ),
  ),
  http.get(
    "*/api/v1/metadata/search",
    jsonApi(
      ({ request }) =>
        permissionDenial("requests.create") ??
        searchTitles(new URL(request.url)),
    ),
  ),
  http.get(
    "*/api/v1/metadata/movies/:id",
    jsonApi(
      ({ params }) =>
        permissionDenial("requests.create") ?? titleDetail("movie", params.id),
    ),
  ),
  http.get(
    "*/api/v1/metadata/series/:id",
    jsonApi(
      ({ params }) =>
        permissionDenial("requests.create") ?? titleDetail("series", params.id),
    ),
  ),
  http.post(
    "*/api/v1/requests/:id/approve",
    jsonApi(
      async ({ params, request }) =>
        permissionDenial("requests.approve") ??
        (await decideRequest(params.id, "approve", request)),
    ),
  ),
  http.post(
    "*/api/v1/requests/:id/decline",
    jsonApi(
      async ({ params, request }) =>
        permissionDenial("requests.approve") ??
        (await decideRequest(params.id, "decline", request)),
    ),
  ),
  http.get(
    "*/api/v1/metadata/providers/tmdb/key",
    jsonApi(
      () =>
        permissionDenial("admin.settings") ??
        HttpResponse.json({ configured: tmdbKeyConfigured }),
    ),
  ),
  http.put(
    "*/api/v1/metadata/providers/tmdb/key",
    jsonApi(
      async ({ request }) =>
        permissionDenial("admin.settings") ?? (await storeTmdbKey(request)),
    ),
  ),
  http.delete(
    "*/api/v1/metadata/providers/tmdb/key",
    jsonApi(() => permissionDenial("admin.settings") ?? removeTmdbKey()),
  ),
];

export const handlers = [
  ...requestHandlers,
  ...roleQuotaHandlers,
  http.get(
    "*/api/v1/playback/now",
    jsonApi(
      () => playbackDenial() ?? HttpResponse.json({ items: mockNowPlaying }),
    ),
  ),
  http.get(
    "*/api/v1/playback/history",
    jsonApi(
      ({ request }) =>
        playbackDenial() ?? playbackHistory(new URL(request.url)),
    ),
  ),
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
