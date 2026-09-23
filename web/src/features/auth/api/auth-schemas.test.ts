import { expect, it } from "@jest/globals";
import { readFileSync } from "node:fs";
import {
  isKnownPermission,
  knownPermissions,
  loginInputSchema,
  loginRequestSchema,
  sessionResponseSchema,
} from "./auth-schemas.js";

it("trims the username and keeps the password verbatim", () => {
  expect(
    loginInputSchema.parse({ username: "  admin ", password: " p w " }),
  ).toEqual({ username: "admin", password: " p w " });
});

it.each([
  ["empty username", { username: "   ", password: "x" }, "Enter your username"],
  [
    "empty password",
    { username: "admin", password: "" },
    "Enter your password",
  ],
  [
    "username over 64",
    { username: "a".repeat(65), password: "x" },
    "Username is too long",
  ],
  [
    "password over 1024",
    { username: "admin", password: "x".repeat(1025) },
    "Password is too long",
  ],
])("rejects %s with a field message", (_label, input, message) => {
  const result = loginInputSchema.safeParse(input);

  expect(result.success).toBe(false);
  expect(result.error?.issues.map((issue) => issue.message)).toContain(message);
});

it("accepts values exactly at the length bounds", () => {
  const input = { username: "a".repeat(64), password: "x".repeat(1024) };

  expect(loginInputSchema.safeParse(input).success).toBe(true);
  expect(loginRequestSchema.safeParse(input).success).toBe(true);
});

it("holds the wire request to exactly two fields", () => {
  const extra = { username: "admin", password: "x", remember: true };

  expect(loginRequestSchema.safeParse(extra).success).toBe(false);
  expect(loginRequestSchema.safeParse({ username: "admin" }).success).toBe(
    false,
  );
  expect(loginRequestSchema.safeParse(null).success).toBe(false);
});

const account = {
  id: "0b6c3d2e-1111-4a2b-9c3d-000000000001",
  username: "admin",
};

// Compatibility policy: a field the UI does not know is dropped, not fatal.
it("strips unknown fields from the session response but requires the account", () => {
  expect(
    sessionResponseSchema.parse({
      account: { ...account, role: "owner" },
      roles: ["admin"],
      permissions: ["admin.roles"],
      extra: 1,
    }),
  ).toEqual({ account, roles: ["admin"], permissions: ["admin.roles"] });
  expect(sessionResponseSchema.safeParse({}).success).toBe(false);
});

it("mirrors the account contract: a UUID id and a canonical username", () => {
  const session = { roles: [], permissions: [] };
  const accepts = (candidate: Record<string, unknown>) =>
    sessionResponseSchema.safeParse({ ...session, account: candidate }).success;

  expect(accepts({ id: "1", username: "admin" })).toBe(false);
  expect(accepts({ ...account, username: "ab" })).toBe(false);
  expect(accepts({ ...account, username: "a".repeat(65) })).toBe(false);
  expect(accepts({ ...account, username: ".admin" })).toBe(false);
  expect(accepts({ ...account, username: "ad min" })).toBe(false);
  expect(accepts({ ...account, username: "josé.w-1" })).toBe(true);
});

it("requires the authorization lists and non-empty permission names", () => {
  expect(sessionResponseSchema.safeParse({ account }).success).toBe(false);
  expect(
    sessionResponseSchema.safeParse({ account, roles: [], permissions: [""] })
      .success,
  ).toBe(false);
  expect(
    sessionResponseSchema.safeParse({
      account,
      roles: [],
      permissions: ["invites.revoke"],
    }).success,
  ).toBe(true);
});

it("knows every permission the OpenAPI contract enumerates, in its order", () => {
  // Jest runs from web/; the contract lives beside it.
  const contract = readFileSync("../api/openapi.yaml", "utf8");
  const enumBlock =
    /Permission:\n(?:.*\n)*?\s+enum:\n((?:\s+- [a-z.]+\n)+)/.exec(contract);
  const fromContract = enumBlock?.[1]
    ?.trim()
    .split("\n")
    .map((line) => line.trim().replace(/^- /, ""));
  expect(fromContract).toEqual([...knownPermissions]);
});

it("separates known permissions from ones a newer server may add", () => {
  expect(isKnownPermission("admin.roles")).toBe(true);
  expect(isKnownPermission("invites.revoke")).toBe(false);
  expect(isKnownPermission("")).toBe(false);
});
