import { expect, it } from "@jest/globals";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http } from "msw";
import { setMockPermissions, signInMockSession } from "../mocks/handlers.js";
import { renderApp } from "../test/render-app.js";
import { server } from "../test/server.js";

const CABIN = "3d7f1a2b-0000-4000-8000-000000000001";

async function openExclusions() {
  signInMockSession();
  const rendered = renderApp(`/admin/media-servers/${CABIN}/exclusions`);
  const form = await screen.findByRole("form", { name: "Exclusions" });
  return { ...rendered, form };
}

it("reaches a server's exclusions from the media servers table", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/admin/media-servers");
  const table = await screen.findByRole("table", {
    name: "Media servers, ordered by name",
  });
  await user.click(
    within(table).getByRole("link", { name: "Exclusions of Cabin" }),
  );
  expect(
    await screen.findByRole("heading", { name: "Exclusions", level: 1 }),
  ).toBeVisible();
  expect(document.title).toBe("Exclusions · Cabin | Bloom");
});

it("offers the server's people and libraries and saves the choice", async () => {
  const user = userEvent.setup();
  let sent: unknown;
  server.use(
    http.put(
      `*/api/v1/media-servers/${CABIN}/exclusions`,
      async ({ request }) => {
        sent = await request.clone().json();
        return Response.json({
          media_server_id: CABIN,
          excluded_media_user_ids: ["u-carol"],
          excluded_library_ids: ["lib-shows"],
        });
      },
    ),
  );
  const { form } = await openExclusions();
  const people = within(form).getByRole("group", { name: "People left out" });
  const libraries = within(form).getByRole("group", {
    name: "Libraries left out",
  });
  expect(
    within(people).getByRole("checkbox", { name: "alice" }),
  ).not.toBeChecked();

  await user.click(within(people).getByRole("checkbox", { name: "carol" }));
  await user.click(within(libraries).getByRole("checkbox", { name: "Shows" }));
  await user.click(
    within(form).getByRole("button", { name: "Save exclusions" }),
  );

  expect(await screen.findByText("Exclusions saved.")).toHaveRole("status");
  expect(sent).toEqual({
    excluded_media_user_ids: ["u-carol"],
    excluded_library_ids: ["lib-shows"],
  });
  const saved = await screen.findByRole("form", { name: "Exclusions" });
  expect(within(saved).getByRole("checkbox", { name: "carol" })).toBeChecked();
  expect(
    within(saved).getByRole("checkbox", { name: "alice" }),
  ).not.toBeChecked();
});

it("keeps a stored id the lists no longer offer", async () => {
  server.use(
    http.get(`*/api/v1/media-servers/${CABIN}/exclusions`, () =>
      Response.json({
        media_server_id: CABIN,
        excluded_media_user_ids: ["u-gone"],
        excluded_library_ids: [],
      }),
    ),
  );
  const { form } = await openExclusions();
  const people = within(form).getByRole("group", { name: "People left out" });
  expect(
    within(people).getByRole("checkbox", { name: "u-gone" }),
  ).toBeChecked();
});

it("shows not found for an unknown server or a bad address", async () => {
  signInMockSession();
  renderApp("/admin/media-servers/not-a-uuid/exclusions");
  expect(
    await screen.findByRole("heading", { name: "Page not found", level: 1 }),
  ).toBeVisible();
});

it("refuses the page without admin.settings", async () => {
  setMockPermissions(["stats.read.all"]);
  signInMockSession();
  renderApp(`/admin/media-servers/${CABIN}/exclusions`);
  expect(
    await screen.findByRole("heading", { name: "Access denied", level: 1 }),
  ).toBeVisible();
});
