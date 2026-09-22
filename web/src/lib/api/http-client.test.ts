import { expect, it, jest } from "@jest/globals";
import { delay, HttpResponse, http } from "msw";
import { z } from "zod/v4";
import { server } from "../../test/server.js";
import type { ApiError } from "./errors.js";
import { ApiClient } from "./http-client.js";

const client = new ApiClient(new URL("http://localhost/"));

it("rejects a success response that fails its Zod contract", async () => {
  server.use(http.get("*/malformed", () => HttpResponse.json({ id: 42 })));

  await expect(
    client.requestJson("malformed", z.object({ id: z.uuid() })),
  ).rejects.toMatchObject({ kind: "invalid-response" });
});

it("rejects a success response that is not JSON", async () => {
  server.use(http.get("*/html", () => HttpResponse.html("<p>hello</p>")));

  await expect(client.requestJson("html", z.object({}))).rejects.toMatchObject({
    kind: "invalid-response",
  });
});

it("rejects a response that declares a body over the size cap", async () => {
  server.use(
    http.get(
      "*/large",
      () =>
        new HttpResponse("{}", {
          headers: {
            "content-type": "application/json",
            "content-length": "1000001",
          },
        }),
    ),
  );

  await expect(client.requestJson("large", z.object({}))).rejects.toMatchObject(
    { kind: "invalid-response" },
  );
});

it("maps the backend error envelope without exposing unchecked fields", async () => {
  server.use(
    http.get("*/problem", () =>
      HttpResponse.json(
        {
          code: "forbidden",
          message: "You cannot read this resource",
          request_id: "req-123",
          internal_stack: "secret",
        },
        { status: 403 },
      ),
    ),
  );

  const request = client.requestJson("problem", z.object({ ok: z.boolean() }));

  await expect(request).rejects.toEqual(
    expect.objectContaining({
      kind: "http",
      message: "You cannot read this resource",
      status: 403,
      code: "forbidden",
      requestId: "req-123",
    } satisfies Partial<ApiError>),
  );
});

it("reads a Retry-After delay from a rate-limited response", async () => {
  server.use(
    http.get("*/limited", () =>
      HttpResponse.json(
        { code: "rate_limited", message: "slow down", request_id: "req-9" },
        { status: 429, headers: { "retry-after": "17" } },
      ),
    ),
  );

  await expect(
    client.requestJson("limited", z.object({})),
  ).rejects.toMatchObject({ kind: "http", status: 429, retryAfterSeconds: 17 });
});

it("ignores a Retry-After header it cannot parse", async () => {
  server.use(
    http.get("*/limited", () =>
      HttpResponse.json(
        { code: "rate_limited", message: "slow down" },
        {
          status: 429,
          headers: { "retry-after": "Wed, 21 Oct 2026 07:28:00 GMT" },
        },
      ),
    ),
  );

  await expect(
    client.requestJson("limited", z.object({})),
  ).rejects.toMatchObject({ status: 429, retryAfterSeconds: undefined });
});

it("maps a non-envelope HTTP failure to a safe status message", async () => {
  server.use(
    http.get("*/broken", () => new HttpResponse("oops", { status: 500 })),
  );

  await expect(
    client.requestJson("broken", z.object({})),
  ).rejects.toMatchObject({
    kind: "http",
    status: 500,
    message: "Request failed with status 500",
  });
});

it("maps a caller abort separately from network failure", async () => {
  const controller = new AbortController();
  controller.abort();

  await expect(
    client.requestJson("api/v1/version", z.object({}), {
      signal: controller.signal,
    }),
  ).rejects.toMatchObject({ kind: "aborted" });
});

it("aborts a request that exceeds the client timeout", async () => {
  server.use(
    http.get("*/slow", async () => {
      await delay("infinite");
      return HttpResponse.json({});
    }),
  );
  const impatientClient = new ApiClient(new URL("http://localhost/"), 5);

  await expect(
    impatientClient.requestJson("slow", z.object({})),
  ).rejects.toMatchObject({ kind: "aborted" });
});

it("fails a request that no MSW handler covers", async () => {
  // MSW reports the unhandled request through console.error before failing it.
  jest.spyOn(console, "error").mockImplementation(() => undefined);

  await expect(
    client.requestJson("api/v1/unhandled", z.object({})),
  ).rejects.toMatchObject({ kind: "network" });
});

it("refuses a base URL that is not HTTP", () => {
  expect(() => new ApiClient(new URL("ftp://localhost/"))).toThrow(TypeError);
});
