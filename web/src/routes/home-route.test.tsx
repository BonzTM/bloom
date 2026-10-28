import { expect, it } from "@jest/globals";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import {
  envelope,
  jsonApi,
  mockRecentlyAdded,
  setMockPermissions,
  signInMockSession,
} from "../mocks/handlers.js";
import { renderApp } from "../test/render-app.js";
import { server } from "../test/server.js";

const ROW_ORDER = [
  "Recently added",
  "Trending",
  "Popular movies",
  "Movie genres",
  "Upcoming movies",
  "Studios",
  "Popular series",
  "Series genres",
  "Upcoming series",
  "Networks",
];

it("lays out the home rows in Seerr's order", async () => {
  signInMockSession();
  renderApp();
  await screen.findByRole("list", { name: "Networks" });
  const headings = screen
    .getAllByRole("heading", { level: 2 })
    .map((heading) => heading.textContent);
  expect(headings).toEqual(["Playing now", ...ROW_ORDER, "My requests"]);
  // Each row is a region named by its heading, with arrow buttons.
  for (const name of ROW_ORDER) {
    await screen.findByRole("list", { name });
    const region = screen.getByRole("region", { name });
    expect(
      within(region).getByRole("button", { name: `Scroll ${name} back` }),
    ).toBeInTheDocument();
    expect(
      within(region).getByRole("button", { name: `Scroll ${name} forward` }),
    ).toBeInTheDocument();
  }
});

it("links each title row to its full list", async () => {
  signInMockSession();
  renderApp();
  const expected: readonly (readonly [string, string])[] = [
    ["Trending", "/browse/trending"],
    ["Popular movies", "/browse/movies/popular"],
    ["Upcoming movies", "/browse/movies/upcoming"],
    ["Popular series", "/browse/series/popular"],
    ["Upcoming series", "/browse/series/upcoming"],
  ];
  for (const [row, href] of expected) {
    expect(
      await screen.findByRole("link", { name: `See all ${row}` }),
    ).toHaveAttribute("href", href);
  }
});

it("marks each poster with its kind and what this account asked for", async () => {
  signInMockSession();
  renderApp();
  const trending = await screen.findByRole("list", { name: "Trending" });
  // The mock repeats its small catalogue; the first copy carries the real
  // id and therefore this account's requests.
  const [inception] = within(trending).getAllByRole("link", {
    name: "Inception (2010) Movie, Available",
  });
  expect(inception).toHaveAttribute("href", "/requests/movies/27205");
  expect(
    within(trending).getAllByRole("link", {
      name: "The Matrix (1999) Movie, Processing",
    }).length,
  ).toBeGreaterThan(0);
  const [arrival] = within(trending).getAllByRole("link", {
    name: "The Arrival (2021) Series",
  });
  expect(arrival).toHaveTextContent("Series");
  // A declined request carries no mark, the way Seerr shows it.
  const popular = screen.getByRole("list", { name: "Popular movies" });
  expect(
    within(popular).getAllByRole("link", { name: "The Prestige (2006) Movie" })
      .length,
  ).toBeGreaterThan(0);
});

it("grows a row a page at a time", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp();
  const trending = await screen.findByRole("list", { name: "Trending" });
  expect(within(trending).getAllByRole("listitem")).toHaveLength(21);
  await user.click(
    within(trending).getByRole("button", { name: "More trending" }),
  );
  await waitFor(() => {
    expect(within(trending).getAllByRole("listitem")).toHaveLength(41);
  });
});

it("offers genre, studio, and network tiles that open their titles", async () => {
  signInMockSession();
  renderApp();
  const movieGenres = await screen.findByRole("list", { name: "Movie genres" });
  expect(
    await within(movieGenres).findByRole("link", { name: "Action" }),
  ).toHaveAttribute("href", "/browse/movies/genres/28");
  const seriesGenres = await screen.findByRole("list", {
    name: "Series genres",
  });
  expect(
    await within(seriesGenres).findByRole("link", { name: "Kids" }),
  ).toHaveAttribute("href", "/browse/series/genres/10762");
  const studios = screen.getByRole("list", { name: "Studios" });
  expect(within(studios).getByRole("link", { name: "Disney" })).toHaveAttribute(
    "href",
    "/browse/movies/studios/2",
  );
  expect(within(studios).getAllByRole("link")).toHaveLength(11);
  const networks = screen.getByRole("list", { name: "Networks" });
  expect(
    within(networks).getByRole("link", { name: "Netflix" }),
  ).toHaveAttribute("href", "/browse/series/networks/213");
  expect(within(networks).getAllByRole("link")).toHaveLength(22);
});

it("shows what the library added, newest first, as available", async () => {
  signInMockSession();
  renderApp();
  const recent = await screen.findByRole("list", { name: "Recently added" });
  const links = within(recent).getAllByRole("link");
  expect(links).toHaveLength(mockRecentlyAdded.length);
  // Dune is the catalog's newest title; an administrator opens its item.
  expect(links[0]).toHaveAccessibleName("Dune (2021) Movie, Available");
  expect(links[0]).toHaveAttribute(
    "href",
    "/admin/libraries/3d7f1a2b-0000-4000-8000-000000000001/items/i-11",
  );
  // A title the media server knows by TMDB ID opens its title page.
  expect(
    within(recent).getByRole("link", {
      name: "The Arrival (2021) Series, Available",
    }),
  ).toHaveAttribute("href", "/requests/series/1396");
});

it("sends a requester from a recently added title to its search", async () => {
  setMockPermissions(["requests.create"]);
  signInMockSession();
  renderApp();
  const recent = await screen.findByRole("list", { name: "Recently added" });
  expect(
    within(recent).getByRole("link", { name: "Dune (2021) Movie, Available" }),
  ).toHaveAttribute("href", "/search?q=Dune&kind=movie");
  expect(
    within(recent).getByRole("link", {
      name: "The Arrival (2021) Series, Available",
    }),
  ).toHaveAttribute("href", "/requests/series/1396");
});

it("shows recently added titles without links to an account that cannot request", async () => {
  setMockPermissions(["requests.read.own"]);
  signInMockSession();
  renderApp();
  const recent = await screen.findByRole("list", { name: "Recently added" });
  expect(within(recent).queryByRole("link")).not.toBeInTheDocument();
  expect(within(recent).getByText("Dune (2021)")).toBeVisible();
  expect(within(recent).getByText("The Arrival (2021)")).toBeVisible();
  // Discovery rows lead to title pages, which need requests.create.
  expect(
    screen.queryByRole("region", { name: "Trending" }),
  ).not.toBeInTheDocument();
});

it("explains an empty library and a failed load", async () => {
  server.use(
    http.get(
      "*/api/v1/library/recently-added",
      jsonApi(() => HttpResponse.json({ items: [] })),
    ),
  );
  signInMockSession();
  const { unmount } = renderApp();
  expect(await screen.findByText(/Nothing has been added yet/)).toBeVisible();
  unmount();

  server.use(
    http.get(
      "*/api/v1/library/recently-added",
      jsonApi(() => envelope(500, "internal", "boom")),
    ),
  );
  renderApp();
  expect(
    await screen.findByText("Recently added titles could not be loaded."),
  ).toBeVisible();
});

it("keeps every playing-now card a poster, using the series art for an episode", async () => {
  signInMockSession();
  renderApp();
  const playing = await screen.findByRole("list", { name: "Playing now" });
  const images = within(playing).getAllByRole("presentation", {
    hidden: true,
  });
  expect(images.length).toBeGreaterThan(1);
  for (const image of images) {
    expect(image).toHaveClass("item-image-poster");
  }
  expect(
    images.some((image) =>
      image.getAttribute("src")?.includes("/items/s-fringe/image"),
    ),
  ).toBe(true);
});
