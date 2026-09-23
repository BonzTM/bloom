import { expect, it } from "@jest/globals";
import { setMockPermissions, signInMockSession } from "./handlers.js";

// The mock server is the contract the UI is tested against, so its own
// answers are pinned here: authorization first, then query validation, then
// paging.
function roles(query = ""): Promise<Response> {
  return fetch(`http://localhost/api/v1/roles${query}`, {
    headers: { accept: "application/json" },
  });
}

async function body(response: Response): Promise<unknown> {
  return (await response.json()) as unknown;
}

it("refuses a signed-out visitor with 401", async () => {
  const response = await roles();
  expect(response.status).toBe(401);
  expect(await body(response)).toMatchObject({ code: "unauthorized" });
});

it("refuses an account without admin.roles with 403", async () => {
  setMockPermissions(["admin.settings"]);
  signInMockSession();
  const response = await roles();
  expect(response.status).toBe(403);
  expect(await body(response)).toMatchObject({ code: "forbidden" });
});

it.each([
  ["?cursor=", "an empty cursor"],
  ["?cursor=nonsense", "an unknown cursor"],
  [`?cursor=offset:${"9".repeat(84)}`, "a cursor over 86 characters"],
  ["?page_size=0", "a zero page size"],
  ["?page_size=1.5", "a fractional page size"],
  ["?page_size=ten", "a textual page size"],
  ["?page_size=1e2", "an exponent page size"],
  ["?page_size=0x10", "a hexadecimal page size"],
  ["?page_size=%201", "a padded page size"],
  ["?page_size=1.0", "a decimal-point page size"],
  ["?page_size=+1", "a signed page size"],
  [
    "?page_size=9223372036854775808",
    "a page size beyond a signed 64-bit integer",
  ],
  ["?page_size=99999999999999999999", "a twenty-digit value beyond the range"],
  ["?page_size=1&page_size=2", "a repeated page size"],
  ["?cursor=offset:50&cursor=offset:50", "a repeated cursor"],
])("answers 422 to %s (%s)", async (query) => {
  signInMockSession();
  const response = await roles(query);
  expect(response.status).toBe(422);
  expect(await body(response)).toMatchObject({ code: "validation_failed" });
});

it("pages 50 roles at a time by default and ends with an empty cursor", async () => {
  signInMockSession();
  const first = (await body(await roles())) as {
    items: { name: string }[];
    next_cursor: string;
  };
  expect(first.items).toHaveLength(50);
  expect(first.next_cursor).toBe("offset:50");
  const second = (await body(await roles("?cursor=offset:50"))) as {
    items: { name: string }[];
    next_cursor: string;
  };
  expect(second.items.map((item) => item.name)).toEqual(["member", "reviewer"]);
  expect(second.next_cursor).toBe("");
  const names = [...first.items, ...second.items].map((item) => item.name);
  expect(names[0]).toBe("admin");
  expect(names).toEqual([...names].sort());
});

it("ignores parameters the server does not read", async () => {
  signInMockSession();
  expect((await roles("?other=1")).status).toBe(200);
});

it("honours page_size and clamps it to 100", async () => {
  signInMockSession();
  const padded = (await body(
    await roles("?page_size=00000000000000000001"),
  )) as {
    items: unknown[];
    next_cursor: string;
  };
  expect(padded.items).toHaveLength(1);
  const one = (await body(await roles("?page_size=1"))) as {
    items: unknown[];
    next_cursor: string;
  };
  expect(one.items).toHaveLength(1);
  expect(one.next_cursor).toBe("offset:1");
  for (const size of [
    "500",
    "9999999999",
    "9223372036854775807",
    "00000000000000000000500",
  ]) {
    const all = (await body(await roles(`?page_size=${size}`))) as {
      items: unknown[];
      next_cursor: string;
    };
    expect(all.items).toHaveLength(52);
    expect(all.next_cursor).toBe("");
  }
});
