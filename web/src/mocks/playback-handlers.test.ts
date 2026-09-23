import { expect, it } from "@jest/globals";
import { setMockPermissions, signInMockSession } from "./handlers.js";

// The mock server is the contract the UI is tested against, so its playback
// answers are pinned here: authorization, then the history filter and its
// validation.
function history(query = ""): Promise<Response> {
  return fetch(`http://localhost/api/v1/playback/history${query}`, {
    headers: { accept: "application/json" },
  });
}

async function json(response: Response): Promise<unknown> {
  return (await response.json()) as unknown;
}

it("refuses a signed-out visitor and an account without stats.read.all", async () => {
  expect((await history()).status).toBe(401);
  setMockPermissions(["admin.roles"]);
  signInMockSession();
  const response = await history();
  expect(response.status).toBe(403);
  expect(await json(response)).toMatchObject({ code: "forbidden" });
  expect(
    (
      await fetch("http://localhost/api/v1/playback/now", {
        headers: { accept: "application/json" },
      })
    ).status,
  ).toBe(403);
});

it("filters history by server", async () => {
  signInMockSession();
  const page = (await json(
    await history("?media_server_id=3d7f1a2b-0000-4000-8000-000000000002"),
  )) as { items: { username: string }[] };
  expect(page.items.map((item) => item.username)).toEqual(["bob"]);
});

it.each([
  ["?media_server_id=nope", "a server id that is not a UUID"],
  [
    "?media_server_id=3d7f1a2b-0000-4000-8000-000000000001&media_server_id=3d7f1a2b-0000-4000-8000-000000000002",
    "a repeated server id",
  ],
  ["?cursor=", "an empty cursor"],
  ["?page_size=0", "a zero page size"],
])("answers 422 to %s (%s)", async (query) => {
  signInMockSession();
  const response = await history(query);
  expect(response.status).toBe(422);
  expect(await json(response)).toMatchObject({ code: "validation_failed" });
});
