import { z } from "zod/v4";

// Wire contracts for the session endpoints under `/api/v1/auth`. Unknown
// response fields are stripped so the backend can extend payloads without
// breaking the UI.
// Response schemas strip fields they do not know. The contract declares no
// extra fields today; stripping is the UI's compatibility policy so a server
// that adds a field ships before the UI that reads it (see web/README.md).
// `AccountSummary` from api/openapi.yaml: a UUID and a canonical PRECIS
// UsernameCaseMapped username of 3 to 64 characters.
const USERNAME_PATTERN = /^[\p{L}\p{Nd}](?:[\p{L}\p{Nd}._-]*[\p{L}\p{Nd}])?$/u;

const accountSchema = z.object({
  id: z.uuid(),
  username: z.string().min(3).max(64).regex(USERNAME_PATTERN),
});

export type Account = z.output<typeof accountSchema>;

// The `Permission` enum from api/openapi.yaml, in the contract's order. The
// server may only add values, so the wire type below accepts any non-empty
// name: a newer server must not break an older UI. Fixtures and gates are
// typed with `KnownPermission` so a drifted name fails the build.
export const knownPermissions = [
  "users.read",
  "users.invite",
  "users.manage",
  "requests.read.own",
  "requests.create",
  "requests.approve",
  "stats.read.own",
  "stats.read.all",
  "admin.settings",
  "admin.roles",
] as const;

export type KnownPermission = (typeof knownPermissions)[number];

export const permissionSchema = z.string().min(1);

export type Permission = z.output<typeof permissionSchema>;

export function isKnownPermission(
  permission: Permission,
): permission is KnownPermission {
  return (knownPermissions as readonly string[]).includes(permission);
}

// `CurrentAccountResponse`: what `POST /api/v1/auth/login` and
// `GET /api/v1/auth/me` both answer, so the UI never needs a second request
// to learn what the person may do.
export const sessionResponseSchema = z.object({
  account: accountSchema,
  roles: z.array(z.string()),
  permissions: z.array(permissionSchema),
});

export type Session = z.output<typeof sessionResponseSchema>;

const USERNAME_MAX_LENGTH = 64;
const PASSWORD_MAX_LENGTH = 1024;

// Client-side validation for the sign-in form. It only guards against empty or
// absurdly long input; the server decides whether the credentials are right.
export const loginInputSchema = z.object({
  username: z
    .string()
    .trim()
    .min(1, "Enter your username")
    .max(USERNAME_MAX_LENGTH, "Username is too long"),
  password: z
    .string()
    .min(1, "Enter your password")
    .max(PASSWORD_MAX_LENGTH, "Password is too long"),
});

export type LoginInput = z.output<typeof loginInputSchema>;

// The exact request body `POST /api/v1/auth/login` accepts: no extra fields,
// nothing already trimmed away. Used by the mock server to hold the client to
// the contract.
export const loginRequestSchema = z.strictObject({
  username: z.string().min(1).max(USERNAME_MAX_LENGTH),
  password: z.string().min(1).max(PASSWORD_MAX_LENGTH),
});
