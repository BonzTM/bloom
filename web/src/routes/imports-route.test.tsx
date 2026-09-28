import { expect, it, jest } from "@jest/globals";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import {
  envelope,
  IMPORT_RUNNING_ID,
  jsonApi,
  setMockPermissions,
  signInMockSession,
} from "../mocks/handlers.js";
import { renderApp } from "../test/render-app.js";
import { server } from "../test/server.js";

async function openImports(): Promise<HTMLElement> {
  signInMockSession();
  renderApp("/admin/imports");
  return screen.findByRole("table", { name: "Imports, newest first" });
}

it("reaches the imports page from the sidebar and the admin index", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/admin");
  await user.click(await screen.findByRole("link", { name: "Imports" }));
  expect(
    await screen.findByRole("heading", { name: "Imports", level: 1 }),
  ).toBeVisible();
  expect(document.title).toBe("Imports | Bloom");
  expect(
    screen.getByRole("link", { name: "Administration: Imports" }),
  ).toHaveAttribute("aria-current", "page");
});

it("denies the page without admin.settings", async () => {
  setMockPermissions(["admin.roles"]);
  signInMockSession();
  renderApp("/admin/imports");
  expect(
    await screen.findByRole("heading", { name: "Access denied" }),
  ).toBeVisible();
});

it("lists jobs with their source, server, state, and counters", async () => {
  const table = await openImports();
  const running = within(table).getByRole("row", { name: /^Bloom export / });
  expect(running).toHaveTextContent("Living room");
  expect(running).toHaveTextContent("Running");
  expect(running).toHaveTextContent("1,500");
  expect(running).toHaveTextContent("1,420");
  expect(running).toHaveTextContent("60");
  expect(
    within(running).getByRole("button", { name: /^Cancel Bloom export/ }),
  ).toBeVisible();
  const done = within(table).getByRole("row", {
    name: /^Playback Reporting /,
  });
  expect(done).toHaveTextContent("Cabin");
  expect(done).toHaveTextContent("Completed");
  expect(done).toHaveTextContent("2026-09-26 09:07");
  expect(within(done).queryByRole("button")).not.toBeInTheDocument();
});

it("starts a Playback Reporting import for the chosen server", async () => {
  const user = userEvent.setup();
  const table = await openImports();
  const form = screen.getByRole("form", { name: "Start an import" });
  await user.selectOptions(within(form).getByLabelText("Server"), "Cabin");
  await user.click(within(form).getByRole("button", { name: "Start import" }));
  const rows = await within(table).findAllByRole("row", {
    name: /^Playback Reporting /,
  });
  expect(rows).toHaveLength(2);
  expect(rows[0]).toHaveTextContent("Waiting");
});

it("asks for the server and the file before starting a Bloom export import", async () => {
  const user = userEvent.setup();
  await openImports();
  const form = screen.getByRole("form", { name: "Start an import" });
  await user.click(
    within(form).getByLabelText("A Bloom export file for that server"),
  );
  await user.click(within(form).getByRole("button", { name: "Start import" }));
  expect(within(form).getByLabelText("Server")).toHaveAccessibleDescription(
    "Choose the server the history belongs to.",
  );
  expect(
    within(form).getByLabelText("Export file"),
  ).toHaveAccessibleDescription(/Choose the export file\./);
});

it("uploads a Bloom export as multipart and shows the new job", async () => {
  const user = userEvent.setup();
  // The test runtime's fetch cannot encode multipart bodies, so the body is
  // inspected on the way out and the API answer is fixed.
  const bodies: unknown[] = [];
  const realFetch = globalThis.fetch.bind(globalThis);
  const spy = jest
    .spyOn(globalThis, "fetch")
    .mockImplementation((input, init) => {
      if (init?.method === "POST" && init.body instanceof FormData) {
        bodies.push(init.body);
      }
      return realFetch(input, init);
    });
  server.use(
    http.post(
      "*/api/v1/imports",
      jsonApi(() =>
        HttpResponse.json(
          {
            id: "9c1d2e3f-0000-4000-8000-000000000099",
            media_server_id: "3d7f1a2b-0000-4000-8000-000000000001",
            source: "bloom_export",
            state: "pending",
            read: 0,
            imported: 0,
            skipped: 0,
            duplicate: 0,
            last_error: "",
            requested_by: "0b6c3d2e-1111-4a2b-9c3d-000000000001",
            created_at: "2026-09-27T15:00:00Z",
            updated_at: "2026-09-27T15:00:00Z",
          },
          { status: 201 },
        ),
      ),
    ),
  );
  try {
    const table = await openImports();
    const form = screen.getByRole("form", { name: "Start an import" });
    await user.selectOptions(within(form).getByLabelText("Server"), "Cabin");
    await user.click(
      within(form).getByLabelText("A Bloom export file for that server"),
    );
    await user.upload(
      within(form).getByLabelText("Export file"),
      new File(['{"id":"x"}\n'], "watches.jsonl", {
        type: "application/x-ndjson",
      }),
    );
    await user.click(
      within(form).getByRole("button", { name: "Start import" }),
    );
    await waitFor(() => {
      expect(bodies).toHaveLength(1);
    });
    expect(within(table).getAllByRole("row").length).toBeGreaterThan(1);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  } finally {
    spy.mockRestore();
  }
  const body = bodies[0] as FormData;
  expect(body.get("source")).toBe("bloom_export");
  expect(body.get("media_server_id")).toBe(
    "3d7f1a2b-0000-4000-8000-000000000001",
  );
  // The test runtime's FormData turns a File from another realm into text,
  // so only the part's presence can be checked here.
  expect(body.has("file")).toBe(true);
});

it("explains a conflicting import and keeps the form", async () => {
  const user = userEvent.setup();
  await openImports();
  const form = screen.getByRole("form", { name: "Start an import" });
  await user.selectOptions(within(form).getByLabelText("Server"), "Cabin");
  await user.click(
    within(form).getByLabelText("A Bloom export file for that server"),
  );
  await user.upload(
    within(form).getByLabelText("Export file"),
    new File(["{}\n"], "watches.jsonl", { type: "application/x-ndjson" }),
  );
  server.use(
    http.post(
      "*/api/v1/imports",
      jsonApi(() =>
        envelope(409, "import_in_progress", "an import is already active"),
      ),
    ),
  );
  await user.click(within(form).getByRole("button", { name: "Start import" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "An import for that server and source is already waiting or running.",
  );
});

it("cancels a running job", async () => {
  const user = userEvent.setup();
  const table = await openImports();
  await user.click(
    within(table).getByRole("button", { name: /^Cancel Bloom export/ }),
  );
  await waitFor(() => {
    expect(
      within(table).getByRole("row", { name: /^Bloom export / }),
    ).toHaveTextContent("Cancelled");
  });
  expect(IMPORT_RUNNING_ID).toBeTruthy();
});

it("offers the export as one download", async () => {
  await openImports();
  expect(screen.getByRole("link", { name: "Download export" })).toHaveAttribute(
    "href",
    "/api/v1/exports/watches",
  );
});
