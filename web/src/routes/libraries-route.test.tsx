import { expect, it } from "@jest/globals";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setMockPermissions, signInMockSession } from "../mocks/handlers.js";
import { renderApp } from "../test/render-app.js";

const CABIN = "3d7f1a2b-0000-4000-8000-000000000001";

it("reaches the libraries page from the sidebar and shows a card per library", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/admin");
  await user.click(
    await screen.findByRole("link", { name: "Administration: Libraries" }),
  );
  expect(
    await screen.findByRole("heading", { name: "Libraries", level: 1 }),
  ).toBeVisible();
  expect(document.title).toBe("Libraries | Bloom");
  const cabin = await screen.findByRole("list", { name: "Libraries on Cabin" });
  const movies = within(cabin)
    .getByRole("link", { name: "Movies" })
    .closest("li");
  expect(movies).toHaveTextContent("Movies3");
  expect(movies).toHaveTextContent("Plays9");
  expect(movies).toHaveTextContent("Watch time19h");
});

it("denies the pages without stats.read.all", async () => {
  setMockPermissions(["admin.roles"]);
  signInMockSession();
  renderApp("/admin/libraries");
  expect(
    await screen.findByRole("heading", { name: "Access denied" }),
  ).toBeVisible();
});

it("queues a walk from the sync button", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/admin/libraries");
  await user.click(
    await screen.findByRole("button", { name: "Sync now Cabin" }),
  );
  expect(
    await screen.findByText("A full walk of Cabin is queued."),
  ).toBeVisible();
});

it("shows a library's items sorted and lets the order change", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp(`/admin/libraries/${CABIN}/lib-movies`);
  expect(
    await screen.findByRole("heading", { name: "Movies", level: 1 }),
  ).toBeVisible();
  const newest = await screen.findByRole("figure", { name: "Newest items" });
  expect(within(newest).getAllByRole("listitem")[0]).toHaveTextContent(
    "Dune (2021)",
  );
  await user.selectOptions(screen.getByLabelText("Sort by"), "plays");
  const byPlays = await screen.findByRole("figure", { name: "Items by plays" });
  await waitFor(() => {
    expect(within(byPlays).getAllByRole("listitem")[0]).toHaveTextContent(
      "Ronin (1998)",
    );
  });
  expect(
    within(
      screen.getByRole("figure", { name: "Never or long unplayed" }),
    ).getByText("Dune (2021)"),
  ).toBeVisible();
  expect(screen.getByRole("figure", { name: "Plays by genre" })).toBeVisible();
});

it("shows an item with its facts, play summary, and history", async () => {
  signInMockSession();
  renderApp(`/admin/libraries/${CABIN}/items/i-4`);
  expect(
    await screen.findByRole("heading", { name: "Ronin (1998)", level: 1 }),
  ).toBeVisible();
  expect(screen.getByText("2 h 2 min")).toBeVisible();
  expect(screen.getByText("Action, Thriller")).toBeVisible();
  const summary = screen.getByRole("list", { name: "Play summary" });
  expect(summary).toHaveTextContent("Plays6");
  expect(summary).toHaveTextContent("People1");
  const history = await screen.findByRole("table", {
    name: "Watches, newest first",
  });
  expect(within(history).getByRole("row", { name: /^bob / })).toHaveTextContent(
    "2 h 0 min",
  );
});

it("links an episode to its series and lists the series' descendants", async () => {
  signInMockSession();
  renderApp(`/admin/libraries/${CABIN}/items/e-1`);
  expect(
    await screen.findByRole("heading", {
      name: "The Arrival S01E01 · Pilot",
      level: 1,
    }),
  ).toBeVisible();
  expect(screen.getByRole("link", { name: "The Arrival" })).toHaveAttribute(
    "href",
    `/admin/libraries/${CABIN}/items/s-1`,
  );
});
