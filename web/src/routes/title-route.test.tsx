import { expect, it } from "@jest/globals";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http } from "msw";
import {
  envelope,
  jsonApi,
  MISSING_TITLE_ID,
  QUOTA_MOVIE_ID,
  setMockOmdbKeyConfigured,
  setMockPermissions,
  signInMockSession,
} from "../mocks/handlers.js";
import { renderApp } from "../test/render-app.js";
import { server } from "../test/server.js";

it("shows a movie with its poster, overview, and request form", async () => {
  signInMockSession();
  renderApp("/requests/movies/438631");
  expect(
    await screen.findByRole("heading", { name: "Dune (2021)", level: 1 }),
  ).toBeVisible();
  expect(document.title).toBe("Dune | Bloom");
  expect(screen.getByText(/Paul Atreides/)).toBeVisible();
  const form = screen.getByRole("form", { name: "Request Dune" });
  expect(within(form).getByLabelText("Profile")).toHaveValue(
    "9c1d2e3f-0000-4000-8000-000000000001",
  );
  expect(within(form).queryByRole("group")).not.toBeInTheDocument();
});

it("requests a movie and reports the outcome", async () => {
  const user = userEvent.setup();
  setMockPermissions(["requests.create", "requests.read.own"]);
  signInMockSession();
  renderApp("/requests/movies/438631");
  const form = await screen.findByRole("form", { name: "Request Dune" });

  await user.click(within(form).getByRole("button", { name: "Request" }));

  const heading = await screen.findByRole("heading", {
    name: "Requested Dune (2021)",
    level: 2,
  });
  expect(heading).toHaveFocus();
  expect(screen.getByText(/Status: Pending\./)).toBeVisible();
  await user.click(screen.getByRole("link", { name: "See your requests." }));
  const list = await screen.findByRole("list", {
    name: "Your requests, newest first",
  });
  expect(within(list).getAllByRole("listitem")[0]).toHaveTextContent("Dune");
});

it("approves its own request when the account may approve", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/requests/movies/438631");
  const form = await screen.findByRole("form", { name: "Request Dune" });
  await user.click(within(form).getByRole("button", { name: "Request" }));
  expect(await screen.findByText(/Status: Approved\./)).toBeVisible();
});

it("lets a series request pick seasons and refuses none", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/requests/series/1396");
  const form = await screen.findByRole("form", {
    name: "Request The Arrival",
  });
  const seasons = within(form).getByRole("group", { name: "Seasons" });
  expect(within(seasons).queryByLabelText(/Specials/)).not.toBeInTheDocument();
  expect(within(seasons).getByLabelText(/Season 3/)).toBeVisible();

  await user.click(within(form).getByRole("button", { name: "Request" }));

  expect(seasons).toHaveAccessibleDescription("Choose at least one season.");
  expect(within(seasons).getByLabelText(/Season 1/)).toHaveFocus();

  await user.click(within(seasons).getByLabelText(/Season 3/));
  await user.click(within(form).getByRole("button", { name: "Request" }));
  expect(
    await screen.findByRole("heading", {
      name: "Requested The Arrival (2021)",
    }),
  ).toBeVisible();
});

it("explains a season that is already requested", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/requests/series/1396");
  const form = await screen.findByRole("form", {
    name: "Request The Arrival",
  });
  await user.click(within(form).getByLabelText(/Season 2/));
  await user.click(within(form).getByRole("button", { name: "Request" }));
  const alert = await within(form).findByRole("alert");
  expect(alert).toHaveTextContent("This title is already requested.");
  expect(alert).toHaveFocus();
  expect(within(form).getByLabelText(/Season 2/)).toBeChecked();
});

it("explains a reached quota with a link to the requests", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp(`/requests/movies/${QUOTA_MOVIE_ID}`);
  const form = await screen.findByRole("form", { name: "Request Fight Club" });
  await user.click(within(form).getByRole("button", { name: "Request" }));
  const alert = await within(form).findByRole("alert");
  expect(alert).toHaveTextContent("You have reached your request limit");
  expect(
    within(alert).getByRole("link", { name: "See your requests." }),
  ).toHaveAttribute("href", "/requests");
});

it("says when no profile accepts the kind", async () => {
  server.use(
    http.get(
      "*/api/v1/request-profiles",
      jsonApi(() => envelope(500, "internal", "boom")),
    ),
  );
  signInMockSession();
  renderApp("/requests/movies/438631");
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "The request profiles could not be loaded",
  );
});

it("shows a not-found title as unavailable", async () => {
  signInMockSession();
  renderApp(`/requests/movies/${MISSING_TITLE_ID}`);
  expect(
    await screen.findByRole("heading", { name: "Title unavailable" }),
  ).toBeVisible();
  expect(screen.getByRole("alert")).toHaveTextContent(
    "That title was not found.",
  );
});

it("rejects an address that is not a title", async () => {
  signInMockSession();
  renderApp("/requests/books/12");
  expect(
    await screen.findByRole("heading", { name: /not found/i }),
  ).toBeVisible();
});

it("shows a movie's rich detail the way Seerr does", async () => {
  signInMockSession();
  renderApp("/requests/movies/438631");
  await screen.findByRole("heading", { name: "Dune (2021)", level: 1 });
  expect(screen.getByText("Rated")).toBeInTheDocument();
  expect(screen.getByText("155 minutes")).toBeVisible();
  expect(screen.getByText("Science Fiction, Adventure")).toBeVisible();
  expect(screen.getByText("Beyond fear, destiny awaits.")).toBeVisible();
  expect(
    screen.getByRole("link", { name: "Watch trailer on YouTube" }),
  ).toHaveAttribute("href", "https://www.youtube.com/watch?v=n9xhJrPXop4");
  expect(screen.getByText("Director").nextElementSibling).toHaveTextContent(
    "Denis Villeneuve",
  );
  expect(
    screen.getByRole("link", { name: "View full crew on TMDB" }),
  ).toHaveAttribute("href", "https://www.themoviedb.org/movie/438631/cast");
  const keywords = screen.getByRole("list", { name: "Keywords" });
  expect(within(keywords).getAllByRole("listitem")).toHaveLength(2);
});

it("lists a movie's facts in the side card", async () => {
  signInMockSession();
  renderApp("/requests/movies/438631");
  const facts = await screen.findByRole("complementary", { name: "Facts" });
  expect(within(facts).getByText("78%")).toBeVisible();
  const fact = (label: string) =>
    within(facts).getByText(label).nextElementSibling;
  expect(fact("Status")).toHaveTextContent("Released");
  expect(fact("Release dates")).toHaveTextContent(
    "October 22, 2021 (Theatrical)December 3, 2021 (Digital)",
  );
  expect(fact("Revenue")).toHaveTextContent("$402,027,830");
  expect(fact("Budget")).toHaveTextContent("$165,000,000");
  expect(fact("Original language")).toHaveTextContent("English");
  expect(fact("Production country")).toHaveTextContent(
    "United States of America",
  );
  expect(fact("Studios")).toHaveTextContent("Legendary Pictures");
  expect(within(facts).getByRole("img", { name: "Max" })).toHaveAttribute(
    "src",
    "https://image.tmdb.org/t/p/w92/max.jpg",
  );
  expect(within(facts).getByRole("link", { name: "IMDb" })).toHaveAttribute(
    "href",
    "https://www.imdb.com/title/tt1160419/",
  );
  expect(within(facts).queryByText("Networks")).not.toBeInTheDocument();
});

it("shows IMDb, Rotten Tomatoes, and Metacritic scores beside TMDB's", async () => {
  setMockOmdbKeyConfigured(true);
  signInMockSession();
  renderApp("/requests/movies/438631");
  const scores = await screen.findByRole("list", { name: "Scores" });
  await within(scores).findByText("IMDb");
  const items = within(scores)
    .getAllByRole("listitem")
    .map((item) => item.textContent);
  expect(items).toEqual([
    "78% TMDB user score",
    "8.0 IMDb 912.3K votes",
    "83% Rotten Tomatoes critics",
    "74 Metacritic",
  ]);
});

it("shows only TMDB's score when no ratings key is stored", async () => {
  signInMockSession();
  renderApp("/requests/movies/438631");
  const scores = await screen.findByRole("list", { name: "Scores" });
  expect(within(scores).getAllByRole("listitem")).toHaveLength(1);
  expect(within(scores).queryByText("IMDb")).not.toBeInTheDocument();
});

it("keeps the title page when the scores fail", async () => {
  setMockOmdbKeyConfigured(true);
  server.use(
    http.get(
      "*/api/v1/metadata/movies/:id/ratings",
      jsonApi(() => envelope(503, "metadata_provider_failure", "down")),
    ),
  );
  signInMockSession();
  renderApp("/requests/movies/438631");
  const facts = await screen.findByRole("complementary", { name: "Facts" });
  expect(within(facts).getByText("78%")).toBeVisible();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

it("shows the cast and related titles with their state", async () => {
  signInMockSession();
  renderApp("/requests/movies/438631");
  const cast = await screen.findByRole("list", { name: "Cast" });
  const members = within(cast).getAllByRole("figure");
  expect(members[0]).toHaveTextContent("Timothée ChalametPaul");
  expect(members[1]?.querySelector(".cast-photo-placeholder")).not.toBeNull();
  const recommended = screen.getByRole("list", { name: "Recommendations" });
  // Related titles use the home page's poster card and its state marks.
  const heat = within(recommended).getByRole("link", {
    name: "Heat (1995) Movie, Available",
  });
  expect(heat).toHaveAttribute("href", "/requests/movies/949");
  const similar = screen.getByRole("list", { name: "Similar titles" });
  const arrival = within(similar).getByRole("link", {
    name: "The Arrival (2021) Series, Requested",
  });
  expect(arrival).toHaveAttribute("href", "/requests/series/1396");
  expect(
    screen.getByRole("button", { name: "Scroll Similar titles forward" }),
  ).toBeInTheDocument();
});

it("jumps to the request panel from the header", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/requests/movies/438631");
  const request = await screen.findByRole("button", { name: "Request Dune" });
  await user.click(request);
  const panel = screen
    .getByRole("form", { name: "Request Dune" })
    .closest(".title-body");
  expect(panel).toHaveFocus();
});

it("shows a series' creators, networks, and air dates", async () => {
  signInMockSession();
  renderApp("/requests/series/1396");
  await screen.findByRole("heading", { name: "The Arrival (2021)", level: 1 });
  expect(screen.getByText("Creator").nextElementSibling).toHaveTextContent(
    "Ada Creator",
  );
  const facts = screen.getByRole("complementary", { name: "Facts" });
  const fact = (label: string) =>
    within(facts).getByText(label).nextElementSibling;
  expect(fact("Seasons")).toHaveTextContent("3");
  expect(fact("Episodes")).toHaveTextContent("26");
  expect(fact("First aired")).toHaveTextContent("March 1, 2021");
  expect(fact("Next episode")).toHaveTextContent(
    "S4 E1 “Return”, January 2, 2027",
  );
  expect(fact("Networks")).toHaveTextContent("HBO");
  expect(within(facts).queryByText("Budget")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("link", { name: /Watch trailer/ }),
  ).not.toBeInTheDocument();
});
