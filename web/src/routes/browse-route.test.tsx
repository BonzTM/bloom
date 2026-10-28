import { describe, expect, it } from "@jest/globals";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setMockPermissions, signInMockSession } from "../mocks/handlers.js";
import { renderApp } from "../test/render-app.js";
import { readSource } from "./browse-route.js";

describe("readSource", () => {
  it.each([
    ["trending", "trending"],
    ["series/upcoming", "series/upcoming"],
    ["movies/genres/28", { list: "movies/genres", id: 28 }],
    ["series/networks/213", { list: "series/networks", id: 213 }],
    [
      "movies/studios/1000000000",
      { list: "movies/studios", id: 1_000_000_000 },
    ],
  ])("reads %s", (splat, source) => {
    expect(readSource(splat)).toEqual(source);
  });

  it.each([
    "",
    "movies",
    "movies/genres",
    "movies/genres/0",
    "movies/genres/028",
    "movies/genres/1000000001",
    "movies/networks/213",
    "series/studios/2",
    "trending/1",
    "movies/genres/28/extra",
  ])("refuses %s", (splat) => {
    expect(readSource(splat)).toBeUndefined();
  });
});

it("lists a genre's titles by the genre's name and loads more", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/browse/movies/genres/28");
  expect(
    await screen.findByRole("heading", { name: "Action movies", level: 1 }),
  ).toBeVisible();
  expect(document.title).toBe("Action movies | Bloom");
  const grid = screen.getByRole("list", { name: "Action movies" });
  expect(within(grid).getAllByRole("listitem")).toHaveLength(20);
  await user.click(screen.getByRole("button", { name: "Load more" }));
  await waitFor(() => {
    expect(within(grid).getAllByRole("listitem")).toHaveLength(40);
  });
});

it("names a studio's and a network's lists", async () => {
  signInMockSession();
  const { unmount } = renderApp("/browse/movies/studios/420");
  expect(
    await screen.findByRole("heading", { name: "Marvel Studios movies" }),
  ).toBeVisible();
  unmount();
  renderApp("/browse/series/networks/213");
  expect(
    await screen.findByRole("heading", { name: "Netflix series" }),
  ).toBeVisible();
});

it("names a curated list", async () => {
  signInMockSession();
  renderApp("/browse/series/upcoming");
  expect(
    await screen.findByRole("heading", { name: "Upcoming series", level: 1 }),
  ).toBeVisible();
});

it("treats an unknown list as a missing page", async () => {
  signInMockSession();
  renderApp("/browse/movies/genres/abc");
  expect(
    await screen.findByRole("heading", { name: "Page not found" }),
  ).toBeVisible();
});

it("refuses an account that cannot request", async () => {
  setMockPermissions(["requests.read.own"]);
  signInMockSession();
  renderApp("/browse/trending");
  expect(
    await screen.findByRole("heading", { name: "Access denied" }),
  ).toBeVisible();
});
