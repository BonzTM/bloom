import { expect, it } from "@jest/globals";
import {
  ADMIN_PERMISSIONS,
  hasAnyPermission,
  hasPermission,
  permissions,
} from "./permissions.js";

it("finds a granted permission and nothing else", () => {
  expect(hasPermission(["users.read", "admin.roles"], "admin.roles")).toBe(
    true,
  );
  expect(hasPermission(["users.read"], "admin.roles")).toBe(false);
  expect(hasPermission([], "admin.roles")).toBe(false);
});

it("matches exact names only", () => {
  expect(hasPermission(["admin.roles.extra"], "admin.roles")).toBe(false);
  expect(hasPermission(["admin"], "admin.roles")).toBe(false);
});

it("accepts any one of several permissions", () => {
  expect(hasAnyPermission(["admin.settings"], ADMIN_PERMISSIONS)).toBe(true);
  expect(hasAnyPermission(["users.read"], ADMIN_PERMISSIONS)).toBe(false);
  expect(hasAnyPermission(["admin.roles"], [])).toBe(false);
});

it("names every admin permission once", () => {
  expect(new Set(ADMIN_PERMISSIONS).size).toBe(ADMIN_PERMISSIONS.length);
  expect(ADMIN_PERMISSIONS).toContain(permissions.adminRoles);
});
