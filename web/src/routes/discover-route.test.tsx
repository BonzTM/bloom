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

async function search(user: User, query: string): Promise<void> {
  const form = await screen.findByRole("search", { name: "Search titles" });
  await user.clear(within(form).getByLabelText("Title"));
  await user.type(within(form).getByLabelText("Title"), query);
  await user.click(within(form).getByRole("button", { name: "Search" }));
}

it("offers requests from the navigation and the home page", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp();
  expect(
    await screen.findByRole("link", { name: "Open requests" }),
  ).toHaveAttribute("href", "/requests");
  await user.click(await screen.findByRole("link", { name: "Requests" }));
  expect(
    await screen.findByRole("heading", { name: "Requests", level: 1 }),
  ).toBeVisible();
  expect(document.title).toBe("Requests | Bloom");
});

it("hides requests from an account without request permissions", async () => {
  setMockPermissions(["stats.read.own"]);
  signInMockSession();
  renderApp("/requests");
  expect(
    await screen.findByRole("heading", { name: "Access denied" }),
  ).toBeVisible();
  expect(
    screen.queryByRole("link", { name: "Requests" }),
  ).not.toBeInTheDocument();
});

it("searches on submit and shows posters that link to the title", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/requests");
  expect(
    await screen.findByText("Type a title to search The Movie Database."),
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
  const { router } = renderApp("/requests");
  const form = await screen.findByRole("search", { name: "Search titles" });
  await user.type(within(form).getByLabelText("Title"), "the");
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
  const form = await screen.findByRole("search", { name: "Search titles" });
  await user.click(within(form).getByRole("button", { name: "Search" }));
  expect(
    screen.getByText("Type a title to search The Movie Database."),
  ).toBeVisible();
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
    await screen.findByRole("search", { name: "Search titles" }),
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
});
