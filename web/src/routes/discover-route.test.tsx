import { expect, it } from "@jest/globals";
import { screen, waitFor, within } from "@testing-library/react";
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

// Searching starts from the top bar, the way Seerr does it; the in-page
// form appears with the results to refine the kind.
async function search(user: User, query: string): Promise<void> {
  const box = await screen.findByRole("searchbox", {
    name: "Search movies and series",
  });
  await user.clear(box);
  await user.type(box, `${query}{Enter}`);
}

it("offers requests from the navigation and the home page", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp();
  expect(
    await screen.findByRole("link", { name: "Open requests" }),
  ).toHaveAttribute("href", "/requests");
  await user.click(await screen.findByRole("link", { name: "Discover" }));
  expect(
    await screen.findByRole("heading", { name: "Discover", level: 1 }),
  ).toBeVisible();
  expect(document.title).toBe("Discover | Bloom");
});

it("hides requests from an account without request permissions", async () => {
  setMockPermissions(["stats.read.own"]);
  signInMockSession();
  renderApp("/requests");
  expect(
    await screen.findByRole("heading", { name: "Access denied" }),
  ).toBeVisible();
  expect(
    screen.queryByRole("link", { name: "Discover" }),
  ).not.toBeInTheDocument();
});

it("searches on submit and shows posters that link to the title", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/requests");
  expect(
    await screen.findByRole("list", { name: "Trending this week" }),
  ).toBeVisible();

  await search(user, "heat");

  const grid = await screen.findByRole("list", { name: "Results for heat" });
  const link = within(grid).getByRole("link", { name: /Heat \(1995\)/ });
  expect(link).toHaveAttribute("href", "/requests/movies/949");
  expect(
    within(link).getByRole("presentation", { hidden: true }),
  ).toHaveAttribute("src", "https://image.tmdb.org/t/p/w342/heat.jpg");
  expect(screen.getByText("1 result for heat.")).toBeVisible();
});

it("keeps the query in the address and narrows by kind", async () => {
  const user = userEvent.setup();
  signInMockSession();
  const { router } = renderApp("/requests?q=the");
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
  renderApp("/requests");
  await search(user, "dune");
  const grid = await screen.findByRole("list", { name: "Results for dune" });
  expect(within(grid).queryByRole("presentation")).not.toBeInTheDocument();
  expect(within(grid).getByText("D")).toBeVisible();
});

it("says when nothing matches", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/requests");
  await search(user, "zzz");
  expect(await screen.findByText("Nothing matches zzz.")).toBeVisible();
});

it("explains a missing TMDB key and a failing provider", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/requests");
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
  renderApp("/requests");
  const box = await screen.findByRole("searchbox", {
    name: "Search movies and series",
  });
  await user.type(box, "{Enter}");
  expect(
    await screen.findByRole("list", { name: "Trending this week" }),
  ).toBeVisible();
  expect(
    screen.queryByRole("search", { name: "Search titles" }),
  ).not.toBeInTheDocument();
  expect(searches).toBe(0);
});

it("lists the account's own requests with status and seasons", async () => {
  signInMockSession();
  renderApp("/requests");
  const list = await screen.findByRole("list", {
    name: "Your requests, newest first",
  });
  const prestige = within(list)
    .getByRole("link", { name: "The Prestige (2006)" })
    .closest("li");
  expect(prestige).not.toBeNull();
  expect(prestige).toHaveTextContent("Declined");
  expect(prestige).toHaveTextContent("Already on the shelf.");
  expect(
    within(list).queryByRole("link", { name: /The Arrival/ }),
  ).not.toBeInTheDocument();
});

it("shows only the search to an account that cannot read its requests", async () => {
  setMockPermissions(["requests.create"]);
  signInMockSession();
  renderApp("/requests");
  expect(
    await screen.findByRole("searchbox", { name: "Search movies and series" }),
  ).toBeVisible();
  expect(
    await screen.findByRole("list", { name: "Trending this week" }),
  ).toBeVisible();
  expect(
    screen.queryByRole("heading", { name: "My requests" }),
  ).not.toBeInTheDocument();
});

it("shows only the requests to an account that cannot search", async () => {
  setMockPermissions(["requests.read.own"]);
  signInMockSession();
  renderApp("/requests");
  expect(
    await screen.findByRole("heading", { name: "My requests" }),
  ).toBeVisible();
  expect(
    screen.queryByRole("search", { name: "Search titles" }),
  ).not.toBeInTheDocument();
  // Discover rows and request titles lead to title pages, which need
  // requests.create, so neither is offered.
  expect(
    screen.queryByRole("list", { name: "Trending this week" }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("link", { name: "Inception (2010)" }),
  ).not.toBeInTheDocument();
});

it("shows progress for the account's own processing request", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/requests");
  const list = await screen.findByRole("list", {
    name: "Your requests, newest first",
  });
  await user.click(
    within(list).getByRole("button", { name: "Progress of The Matrix (1999)" }),
  );
  expect(
    await within(list).findByLabelText("Progress of The Matrix (1999)"),
  ).toHaveTextContent("75% done");
});

it("shows discover rows with the viewer's request state and loads more", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/requests");
  const trending = await screen.findByRole("list", {
    name: "Trending this week",
  });
  expect(within(trending).getAllByRole("listitem").length).toBeGreaterThan(3);
  expect(screen.getByRole("list", { name: "Popular movies" })).toBeVisible();
  expect(screen.getByRole("list", { name: "Series on the air" })).toBeVisible();
  // The mock repeats its small catalogue to fill a row; only the first
  // copy carries the real id and therefore this account's request.
  const [prestige] = within(
    screen.getByRole("list", { name: "Popular movies" }),
  ).getAllByRole("link", { name: /The Prestige/ });
  expect(prestige).toHaveTextContent("Declined");
  await user.click(
    within(trending).getByRole("button", { name: "More trending this week" }),
  );
  await waitFor(() => {
    expect(within(trending).getAllByRole("listitem").length).toBeGreaterThan(
      20,
    );
  });
});

it("hides the rows while a search is showing", async () => {
  signInMockSession();
  renderApp("/requests?q=heat");
  await screen.findByRole("list", { name: "Results for heat" });
  expect(
    screen.queryByRole("list", { name: "Trending this week" }),
  ).not.toBeInTheDocument();
});
