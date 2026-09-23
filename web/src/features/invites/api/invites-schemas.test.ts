import { expect, it } from "@jest/globals";
import {
  createdInviteSchema,
  createInviteRequestSchema,
  invitesPageSchema,
} from "./invites-schemas.js";

const invite = {
  id: "6a1b2c3d-0000-4000-8000-000000000001",
  media_server_id: "3d7f1a2b-0000-4000-8000-000000000001",
  label: "Family",
  expires_at: "2026-12-31T00:00:00Z",
  max_uses: 5,
  use_count: 1,
  library_ids: [],
  status: "active",
  created_at: "2026-09-20T09:00:00Z",
};

it("accepts a page and strips fields it does not know", () => {
  const page = invitesPageSchema.parse({
    items: [{ ...invite, colour: "red" }],
    next_cursor: "",
    total: 1,
  });
  expect(page).toEqual({ items: [invite], next_cursor: "" });
});

it.each([
  ["an unknown status", { status: "paused" }],
  ["a negative use count", { use_count: -1 }],
  ["a use limit over 1000", { max_uses: 1001 }],
  ["a label over 100 bytes", { label: "é".repeat(51) }],
  ["an id that is not a UUID", { id: "1" }],
])("rejects an invite with %s", (_label, patch) => {
  expect(
    invitesPageSchema.safeParse({
      items: [{ ...invite, ...patch }],
      next_cursor: "",
    }).success,
  ).toBe(false);
});

it("requires a well-formed code and accept path on creation", () => {
  expect(
    createdInviteSchema.safeParse({
      invite,
      code: "abc",
      accept_path: "/invite/abc",
    }).success,
  ).toBe(false);
  const created = createdInviteSchema.parse({
    invite,
    code: "ABCDEFGHIJKLMNOPQRSTUVWXYZ",
    accept_path: "/invite/ABCDEFGHIJKLMNOPQRSTUVWXYZ",
  });
  expect(created.accept_path).toBe("/invite/ABCDEFGHIJKLMNOPQRSTUVWXYZ");
});

it("sends exactly the request the contract accepts", () => {
  const request = {
    media_server_id: invite.media_server_id,
    label: "Family",
  };
  expect(createInviteRequestSchema.parse(request)).toEqual(request);
  expect(
    createInviteRequestSchema.safeParse({ ...request, note: "hidden" }).success,
  ).toBe(false);
  expect(
    createInviteRequestSchema.safeParse({ ...request, label: " Family" })
      .success,
  ).toBe(false);
});

it("bounds library ids to 256 entries of at most 128 bytes each", () => {
  const request = { media_server_id: invite.media_server_id, label: "Family" };
  const ids = (count: number) =>
    Array.from({ length: count }, (_, index) => `lib-${String(index)}`);
  expect(
    createInviteRequestSchema.safeParse({ ...request, library_ids: ids(256) })
      .success,
  ).toBe(true);
  expect(
    createInviteRequestSchema.safeParse({ ...request, library_ids: ids(257) })
      .success,
  ).toBe(false);
  expect(
    createInviteRequestSchema.safeParse({
      ...request,
      library_ids: ["é".repeat(65)],
    }).success,
  ).toBe(false);
});
