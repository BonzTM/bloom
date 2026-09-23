import { expect, it } from "@jest/globals";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http } from "msw";
import {
  envelope,
  jsonApi,
  MISSING_TITLE_ID,
  QUOTA_MOVIE_ID,
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
