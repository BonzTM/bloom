import type { KnownPermission, Permission } from "./api/auth-schemas.js";

// Permission names the UI gates on, typed against the contract's enum so a
// name the server no longer publishes fails the build. A name the server
// adds later never appears here, so a stale entry fails closed.
export const permissions = {
  adminSettings: "admin.settings",
  adminRoles: "admin.roles",
  usersInvite: "users.invite",
  statsReadAll: "stats.read.all",
  statsReadOwn: "stats.read.own",
  requestsApprove: "requests.approve",
  requestsCreate: "requests.create",
  requestsReadOwn: "requests.read.own",
} as const satisfies Record<string, KnownPermission>;

export const ADMIN_PERMISSIONS: readonly KnownPermission[] = [
  permissions.adminSettings,
  permissions.adminRoles,
  permissions.usersInvite,
  permissions.statsReadAll,
  permissions.requestsApprove,
];

// Permissions that open the requests area to a signed-in account.
export const REQUESTS_PERMISSIONS: readonly KnownPermission[] = [
  permissions.requestsCreate,
  permissions.requestsReadOwn,
];

export function hasPermission(
  granted: readonly Permission[],
  required: KnownPermission,
): boolean {
  return granted.includes(required);
}

export function hasAnyPermission(
  granted: readonly Permission[],
  required: readonly KnownPermission[],
): boolean {
  return required.some((permission) => hasPermission(granted, permission));
}
