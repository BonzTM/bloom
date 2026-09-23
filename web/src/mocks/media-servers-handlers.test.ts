import { expect, it } from "@jest/globals";
import {
  BUSY_MEDIA_SERVER_HOST,
  setMockPermissions,
  signInMockSession,
  UNREACHABLE_MEDIA_SERVER_HOST,
} from "./handlers.js";

// The mock server is the contract the UI is tested against, so its media
// server answers are pinned here: authorization, then the registration
// outcomes the real handler can produce.
function register(body: unknown): Promise<Response> {
  return fetch("http://localhost/api/v1/media-servers", {
    method: "POST",
    headers: {
      accept: "application/json",
      "content-type": "application/json",
    },
    body: JSON.stringify(body),
  });
}

async function json(response: Response): Promise<unknown> {
  return (await response.json()) as unknown;
}

const office = {
  kind: "jellyfin",
  name: "Office",
  base_url: "https://office.example",
  api_key: "k3y",
};

it("refuses a signed-out visitor and an account without admin.settings", async () => {
  expect((await register(office)).status).toBe(401);
  setMockPermissions(["admin.roles"]);
  signInMockSession();
  const response = await register(office);
  expect(response.status).toBe(403);
  expect(await json(response)).toMatchObject({ code: "forbidden" });
});

it("treats an omitted override as false, as the contract's default says", async () => {
  signInMockSession();
  const response = await register(office);
  expect(response.status).toBe(201);
  expect(await json(response)).toMatchObject({
    server: { name: "Office", allow_insecure: false },
    info: { name: "Mock Jellyfin" },
  });
});

it.each([
  [
    "plaintext HTTP without the override",
    { base_url: "http://office.example" },
  ],
  ["the override on HTTPS", { allow_insecure: true }],
  ["an unparseable address", { base_url: "http://[" }],
  ["an empty API key", { api_key: "" }],
  ["an unknown field", { note: "hidden" }],
  ["an untrimmed name", { name: "Office " }],
])("answers 422 to %s", async (_label, patch) => {
  signInMockSession();
  const response = await register({ ...office, ...patch });
  expect(response.status).toBe(422);
  expect(await json(response)).toMatchObject({ code: "validation_failed" });
});

it("answers 409 to a name already in use, ignoring case", async () => {
  signInMockSession();
  const response = await register({ ...office, name: "cabin" });
  expect(response.status).toBe(409);
  expect(await json(response)).toMatchObject({ code: "already_exists" });
});

it("answers 502 with the contract's media_server_failure code", async () => {
  signInMockSession();
  const response = await register({
    ...office,
    base_url: `https://${UNREACHABLE_MEDIA_SERVER_HOST}`,
  });
  expect(response.status).toBe(502);
  expect(response.headers.get("retry-after")).toBe("5");
  expect(await json(response)).toMatchObject({ code: "media_server_failure" });
});

it("answers 503 with a Retry-After for a busy server", async () => {
  signInMockSession();
  const response = await register({
    ...office,
    base_url: `https://${BUSY_MEDIA_SERVER_HOST}`,
  });
  expect(response.status).toBe(503);
  expect(response.headers.get("retry-after")).toBe("2");
  expect(await json(response)).toMatchObject({ code: "unavailable" });
});
