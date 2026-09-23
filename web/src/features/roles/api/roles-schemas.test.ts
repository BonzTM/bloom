import { expect, it } from "@jest/globals";
import { rolesCursorSchema, rolesPageSchema } from "./roles-schemas.js";

const role = {
  id: "8e1c2f7a-0000-4000-8000-000000000001",
  name: "admin",
  description: "Everything.",
  built_in: true,
  created_at: "2026-09-01T12:00:00Z",
  permissions: ["admin.roles", "users.read"],
};

it("accepts a page and strips fields it does not know", () => {
  const page = rolesPageSchema.parse({
    items: [{ ...role, colour: "red" }],
    next_cursor: "",
    total: 1,
  });
  expect(page).toEqual({ items: [role], next_cursor: "" });
});

it("accepts a permission name it has never seen, since the catalog only grows", () => {
  const page = rolesPageSchema.parse({
    items: [{ ...role, permissions: ["invites.revoke"] }],
    next_cursor: "",
  });
  expect(page.items[0]?.permissions).toEqual(["invites.revoke"]);
});

it("rejects an empty permission name and a role id that is not a UUID", () => {
  expect(
    rolesPageSchema.safeParse({
      items: [{ ...role, permissions: [""] }],
      next_cursor: "",
    }).success,
  ).toBe(false);
  expect(
    rolesPageSchema.safeParse({
      items: [{ ...role, id: "1" }],
      next_cursor: "",
    }).success,
  ).toBe(false);
});

it("holds the page and cursor to the contract bounds", () => {
  expect(rolesCursorSchema.safeParse("a".repeat(86)).success).toBe(true);
  expect(rolesCursorSchema.safeParse("a".repeat(87)).success).toBe(false);
  expect(rolesCursorSchema.safeParse("").success).toBe(false);
  expect(
    rolesPageSchema.safeParse({
      items: Array.from({ length: 101 }, () => role),
      next_cursor: "",
    }).success,
  ).toBe(false);
});

it("rejects a created_at that is not a timestamp", () => {
  const result = rolesPageSchema.safeParse({
    items: [{ ...role, created_at: "yesterday" }],
    next_cursor: "",
  });
  expect(result.success).toBe(false);
});
