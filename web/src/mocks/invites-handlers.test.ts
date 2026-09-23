import { expect, it } from "@jest/globals";
import {
  REVOKED_INVITE_CODE,
  setMockPermissions,
  signInMockSession,
  TAKEN_INVITE_USERNAME,
  VALID_INVITE_CODE,
} from "./handlers.js";

// The mock server is the contract the UI is tested against, so its invite
// answers are pinned here: authorization, strict requests, and the public
// route's single opaque 404.
const JSON_HEADERS = {
  accept: "application/json",
  "content-type": "application/json",
};

function create(body: unknown): Promise<Response> {
  return fetch("http://localhost/api/v1/invites", {
    method: "POST",
    headers: JSON_HEADERS,
    body: JSON.stringify(body),
  });
}

function preview(code: string): Promise<Response> {
  return fetch(`http://localhost/api/v1/invite/${code}`, {
    headers: { accept: "application/json" },
  });
}

function accept(code: string, body: unknown): Promise<Response> {
  return fetch(`http://localhost/api/v1/invite/${code}/accept`, {
    method: "POST",
    headers: JSON_HEADERS,
    body: JSON.stringify(body),
  });
}

async function json(response: Response): Promise<unknown> {
  return (await response.json()) as unknown;
}

const family = {
  media_server_id: "3d7f1a2b-0000-4000-8000-000000000001",
  label: "Friends",
};

const account = { username: "alice", password: "correct horse battery" };

it("refuses a signed-out visitor and an account without users.invite", async () => {
  expect((await create(family)).status).toBe(401);
  setMockPermissions(["admin.settings"]);
  signInMockSession();
  const response = await create(family);
  expect(response.status).toBe(403);
  expect(await json(response)).toMatchObject({ code: "forbidden" });
});

it.each([
  ["an unknown field", { note: "hidden" }],
  ["an untrimmed label", { label: "Friends " }],
  ["a use limit over 1000", { max_uses: 1001 }],
  ["an expiry that is not a timestamp", { expires_at: "tomorrow" }],
])("answers 422 to %s", async (_label, patch) => {
  signInMockSession();
  const response = await create({ ...family, ...patch });
  expect(response.status).toBe(422);
  expect(await json(response)).toMatchObject({ code: "validation_failed" });
});

it("carries supplied library ids and refuses duplicates", async () => {
  signInMockSession();
  const created = (await json(
    await create({ ...family, library_ids: ["lib-1", "lib-2"] }),
  )) as { invite: { library_ids: string[] } };
  expect(created.invite.library_ids).toEqual(["lib-1", "lib-2"]);
  expect(
    (await create({ ...family, library_ids: ["lib-1", "lib-1"] })).status,
  ).toBe(422);
});

it("answers 404 for a media server that does not exist", async () => {
  signInMockSession();
  const response = await create({
    ...family,
    media_server_id: "3d7f1a2b-0000-4000-8000-0000000000ff",
  });
  expect(response.status).toBe(404);
});

it("answers the same 404 for revoked and malformed codes", async () => {
  for (const code of [
    REVOKED_INVITE_CODE,
    "nope",
    "abcdefghijklmnopqrstuvwxyz",
  ]) {
    const response = await preview(code);
    expect(response.status).toBe(404);
    expect(await json(response)).toMatchObject({ code: "not_found" });
  }
});

it("answers 409 to a taken username", async () => {
  const response = await accept(VALID_INVITE_CODE, {
    ...account,
    username: TAKEN_INVITE_USERNAME,
  });
  expect(response.status).toBe(409);
  expect(await json(response)).toMatchObject({ code: "username_unavailable" });
});

it("uses up a single-use invite after one acceptance", async () => {
  signInMockSession();
  const created = (await json(await create({ ...family, max_uses: 1 }))) as {
    code: string;
  };
  expect((await preview(created.code)).status).toBe(200);
  const first = await accept(created.code, account);
  expect(first.status).toBe(201);
  expect(await json(first)).toMatchObject({
    media_server_name: "Cabin",
    username: "alice",
  });
  expect((await preview(created.code)).status).toBe(404);
  expect((await accept(created.code, account)).status).toBe(404);
});
