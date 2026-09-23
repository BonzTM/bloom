import { expect, it } from "@jest/globals";
import {
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

it("strips unknown fields from the session response but requires the account", () => {
  expect(
    sessionResponseSchema.parse({
      account: { id: "1", username: "admin", role: "owner" },
      extra: 1,
    }),
  ).toEqual({ account: { id: "1", username: "admin" } });
  expect(sessionResponseSchema.safeParse({}).success).toBe(false);
  expect(
    sessionResponseSchema.safeParse({ account: { id: "", username: "x" } })
      .success,
  ).toBe(false);
});
