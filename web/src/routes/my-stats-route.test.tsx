import { expect, it } from "@jest/globals";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http } from "msw";
import {
  jsonApi,
  mockOwnLinks,
  setMockOwnLinks,
  setMockPermissions,
  signInMockSession,
} from "../mocks/handlers.js";
import { renderApp } from "../test/render-app.js";
import { server } from "../test/server.js";

const LIVING_ROOM = "3d7f1a2b-0000-4000-8000-000000000002";

it("offers the page from the main navigation", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp();
  await user.click(await screen.findByRole("link", { name: "My statistics" }));
  expect(
    await screen.findByRole("heading", { name: "My statistics", level: 1 }),
  ).toBeVisible();
  expect(document.title).toBe("My statistics | Bloom");
});

it("hides the page without stats.read.own", async () => {
  setMockPermissions(["stats.read.all"]);
  signInMockSession();
  renderApp("/statistics");
  expect(
    await screen.findByRole("heading", { name: "Access denied" }),
  ).toBeVisible();
  expect(
    screen.queryByRole("link", { name: "My statistics" }),
  ).not.toBeInTheDocument();
});

it("shows the linked user's dashboard and names the link", async () => {
  signInMockSession();
  renderApp("/statistics");
  expect(await screen.findByText("Showing alice on Cabin.")).toBeVisible();
  expect(screen.getByRole("list", { name: "Totals" })).toHaveTextContent(
    "Plays17",
  );
  expect(
    screen.getByRole("figure", { name: "Most watched movies" }),
  ).toBeVisible();
  const watches = screen.getByRole("table", {
    name: "Latest watches, newest first",
  });
  expect(within(watches).getAllByRole("rowheader").length).toBeGreaterThan(0);
  expect(screen.queryByLabelText("Server")).not.toBeInTheDocument();
});

it("explains when no media user is linked", async () => {
  setMockOwnLinks([]);
  signInMockSession();
  renderApp("/statistics");
  expect(
    await screen.findByRole("heading", {
      name: "No media-server user is linked yet",
    }),
  ).toBeVisible();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

it("picks the server when several users are linked", async () => {
  const user = userEvent.setup();
  const requested: string[] = [];
  server.use(
    http.get(
      "*/api/v1/stats/me",
      jsonApi(({ request }) => {
        requested.push(new URL(request.url).search);
        return undefined;
      }),
    ),
  );
  setMockOwnLinks([
    ...mockOwnLinks,
    {
      account_id: "2f5b8c1d-0000-4000-8000-000000000001",
      media_server_id: LIVING_ROOM,
      media_server_name: "Living room",
      media_user_id: "u-bob",
      username: "bob",
      source: "admin",
      created_at: "2026-09-21T10:00:00Z",
      updated_at: "2026-09-21T10:00:00Z",
    },
  ]);
  signInMockSession();
  renderApp("/statistics");
  expect(await screen.findByText("Showing alice on Cabin.")).toBeVisible();

  await user.selectOptions(screen.getByLabelText("Server"), "Living room");

  expect(await screen.findByText("Showing bob on Living room.")).toBeVisible();
  expect(
    requested.some((q) => q.includes(`media_server_id=${LIVING_ROOM}`)),
  ).toBe(true);
});
