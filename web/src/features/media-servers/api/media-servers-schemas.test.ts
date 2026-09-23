import { expect, it } from "@jest/globals";
import {
  mediaServersPageSchema,
  registeredMediaServerSchema,
  registerMediaServerInputSchema,
  registerMediaServerRequestSchema,
} from "./media-servers-schemas.js";

const server = {
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
};

it("accepts a page and strips fields it does not know", () => {
  const page = mediaServersPageSchema.parse({
    items: [{ ...server, colour: "red" }],
    next_cursor: "",
    total: 1,
  });
  expect(page).toEqual({ items: [server], next_cursor: "" });
});

it.each([
  ["a name over 100 bytes", { name: "é".repeat(51) }],
  ["an empty name", { name: "" }],
  ["an address without a scheme", { base_url: "jellyfin.example" }],
  ["an address with another scheme", { base_url: "ftp://jellyfin.example" }],
  ["an address over 2048 bytes", { base_url: `https://${"a".repeat(2048)}` }],
  ["an unknown kind", { kind: "plex" }],
  ["an id that is not a UUID", { id: "1" }],
])("rejects a server with %s", (_label, patch) => {
  expect(
    mediaServersPageSchema.safeParse({
      items: [{ ...server, ...patch }],
      next_cursor: "",
    }).success,
  ).toBe(false);
});

it("requires the probe result alongside the registered server", () => {
  expect(
    registeredMediaServerSchema.safeParse({ server, info: {} }).success,
  ).toBe(false);
  const registered = registeredMediaServerSchema.parse({
    server,
    info: { name: "Mock Jellyfin", version: "10.10.7", id: "srv", extra: 1 },
  });
  expect(registered.info).toEqual({
    name: "Mock Jellyfin",
    version: "10.10.7",
    id: "srv",
  });
});

it("defaults the plaintext override to false when it is omitted", () => {
  const input = registerMediaServerInputSchema.parse({
    kind: "jellyfin",
    name: "Office",
    base_url: "https://office.example",
    api_key: "k3y",
  });
  expect(input.allow_insecure).toBe(false);
});

const request = {
  kind: "jellyfin",
  name: "Office",
  base_url: "https://office.example",
  api_key: "k3y",
};

it("sends exactly the request the contract accepts", () => {
  expect(registerMediaServerRequestSchema.parse(request)).toEqual({
    ...request,
    allow_insecure: false,
  });
});

it.each([
  ["an unknown field", { note: "hidden" }],
  ["an untrimmed name", { name: " Office" }],
  ["a malformed address", { base_url: "https://[" }],
  ["an address without a scheme", { base_url: "office.example" }],
  ["an address with another scheme", { base_url: "ftp://office.example" }],
  ["an empty API key", { api_key: "" }],
  ["an API key with a control character", { api_key: "a\u0007b" }],
])("refuses to send a request with %s", (_label, patch) => {
  expect(
    registerMediaServerRequestSchema.safeParse({ ...request, ...patch })
      .success,
  ).toBe(false);
});
