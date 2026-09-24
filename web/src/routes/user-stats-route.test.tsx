import { expect, it } from "@jest/globals";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { signInMockSession } from "../mocks/handlers.js";
import { renderApp } from "../test/render-app.js";

const ALICE =
  "/admin/statistics/users/3d7f1a2b-0000-4000-8000-000000000001/u-alice";

it("shows one person's totals, charts, and latest watches", async () => {
  signInMockSession();
  renderApp(ALICE);
  expect(
    await screen.findByRole("heading", { name: "alice", level: 1 }),
  ).toBeVisible();
  expect(document.title).toBe("alice | Bloom");
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
  expect(
    screen.getByRole("link", { name: "Back to statistics" }),
  ).toHaveAttribute("href", "/admin/statistics");
});

it("reaches a person from the statistics page", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/admin/statistics");
  const people = await screen.findByRole("list", {
    name: "People with statistics",
  });
  await user.click(within(people).getByRole("link", { name: "bob" }));
  expect(
    await screen.findByRole("heading", { name: "bob", level: 1 }),
  ).toBeVisible();
});

it("shows not found for an unknown person or a bad address", async () => {
  signInMockSession();
  renderApp(
    "/admin/statistics/users/3d7f1a2b-0000-4000-8000-000000000001/u-nobody",
  );
  expect(
    await screen.findByRole("heading", { name: /not found/i }),
  ).toBeVisible();
});

it("rejects a server id that is not a uuid", async () => {
  signInMockSession();
  renderApp("/admin/statistics/users/not-a-uuid/u-alice");
  expect(
    await screen.findByRole("heading", { name: /not found/i }),
  ).toBeVisible();
});
