import { expect, it } from "@jest/globals";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http } from "msw";
import {
  BROKEN_QUERY,
  envelope,
  jsonApi,
  setMockPermissions,
  signInMockSession,
  UNCONFIGURED_QUERY,
} from "../mocks/handlers.js";
import { renderApp } from "../test/render-app.js";
import { server } from "../test/server.js";

type User = ReturnType<typeof userEvent.setup>;

// Searching starts from the top bar, the way Seerr does it; the search page
// shows the results with a form to refine the kind.
async function search(user: User, query: string): Promise<void> {
  const box = await screen.findByRole("searchbox", {
    name: "Search movies and series",
  });
  await user.clear(box);
  await user.type(box, `${query}{Enter}`);
}

it("searches from the top bar on any page and lands on the search page", async () => {
  const user = userEvent.setup();
  signInMockSession();
  const { router } = renderApp();
  await search(user, "heat");

  expect(
    await screen.findByRole("heading", { name: "Search", level: 1 }),
  ).toBeVisible();
  expect(router.state.location.pathname).toBe("/search");
  expect(router.state.location.search).toBe("?q=heat");
  expect(document.title).toBe("Search | Bloom");
  const grid = await screen.findByRole("list", { name: "Results for heat" });
  const link = within(grid).getByRole("link", { name: "Heat (1995) Movie" });
  expect(link).toHaveAttribute("href", "/requests/movies/949");
  expect(
    within(link).getByRole("presentation", { hidden: true }),
  ).toHaveAttribute("src", "https://image.tmdb.org/t/p/w342/heat.jpg");
  expect(screen.getByText("1 result for heat.")).toBeVisible();
  // Search belongs to the home area, which holds discovery.
  const nav = within(
    screen.getByRole("navigation", { name: "Main navigation" }),
  );
  expect(nav.getByRole("link", { name: "Home" })).toHaveAttribute(
    "aria-current",
    "location",
  );
});

it("hides search from an account that cannot request", async () => {
  setMockPermissions(["requests.read.own"]);
  signInMockSession();
  renderApp("/search?q=heat");
  expect(
    await screen.findByRole("heading", { name: "Access denied" }),
  ).toBeVisible();
  expect(
    screen.queryByRole("searchbox", { name: "Search movies and series" }),
  ).not.toBeInTheDocument();
});

it("keeps the query in the address and narrows by kind", async () => {
  const user = userEvent.setup();
  signInMockSession();
  const { router } = renderApp("/search?q=the");
  const form = await screen.findByRole("search", { name: "Search titles" });
  await user.selectOptions(within(form).getByLabelText("Kind"), "Series");
  await user.click(within(form).getByRole("button", { name: "Search" }));

  const grid = await screen.findByRole("list", { name: "Results for the" });
  expect(within(grid).getAllByRole("listitem")).toHaveLength(1);
  expect(
    within(grid).getByRole("link", { name: /The Arrival/ }),
  ).toHaveAttribute("href", "/requests/series/1396");
  expect(router.state.location.search).toBe("?q=the&kind=series");
});

it("shows a placeholder when a title has no poster", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/search");
  await search(user, "dune");
  const grid = await screen.findByRole("list", { name: "Results for dune" });
  expect(within(grid).queryByRole("presentation")).not.toBeInTheDocument();
  expect(within(grid).getByText("D")).toBeVisible();
});

it("asks for a title before searching", async () => {
  signInMockSession();
  renderApp("/search");
  expect(
    await screen.findByText("Type a title to search The Movie Database."),
  ).toBeVisible();
});

it("says when nothing matches", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/search");
  await search(user, "zzz");
  expect(await screen.findByText("Nothing matches zzz.")).toBeVisible();
});

it("explains a missing TMDB key and a failing provider", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/search");
  await search(user, UNCONFIGURED_QUERY);
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Searching needs a TMDB key",
  );
  await search(user, BROKEN_QUERY);
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "The metadata provider answered with something Bloom could not use.",
  );
});

it("sends nothing for an empty query", async () => {
  const user = userEvent.setup();
  let searches = 0;
  server.use(
    http.get(
      "*/api/v1/metadata/search",
      jsonApi(() => {
        searches += 1;
        return envelope(500, "internal", "boom");
      }),
    ),
  );
  signInMockSession();
  const { router } = renderApp();
  const box = await screen.findByRole("searchbox", {
    name: "Search movies and series",
  });
  await user.type(box, "{Enter}");
  expect(await screen.findByRole("list", { name: "Trending" })).toBeVisible();
  expect(router.state.location.pathname).toBe("/");
  expect(searches).toBe(0);
});

it("sends a search made on the old Discover address to the search page", async () => {
  signInMockSession();
  const { router } = renderApp("/requests?q=heat&kind=movie");
  await screen.findByRole("list", { name: "Results for heat" });
  expect(router.state.location.pathname).toBe("/search");
  expect(router.state.location.search).toBe("?q=heat&kind=movie");
});

it("sends the old Discover address home", async () => {
  signInMockSession();
  const { router } = renderApp("/requests");
  expect(await screen.findByRole("list", { name: "Trending" })).toBeVisible();
  expect(router.state.location.pathname).toBe("/");
});
