import { expect, it } from "@jest/globals";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderApp } from "../test/render-app.js";

it("renders the home page with the server version from the API", async () => {
  renderApp();

  expect(
    screen.getByRole("heading", { name: "Bloom", level: 1 }),
  ).toBeVisible();
  expect(screen.getByRole("status")).toHaveTextContent(
    "Loading server version",
  );
  expect(await screen.findByText(/Version 0\.1\.0-dev/)).toBeVisible();
  expect(screen.getByText("0123456")).toBeVisible();
  expect(
    screen.getByRole("heading", { name: "Users & invites", level: 2 }),
  ).toBeVisible();
  expect(
    screen.getByRole("heading", { name: "Statistics", level: 2 }),
  ).toBeVisible();
  expect(
    screen.getByRole("heading", { name: "Requests", level: 2 }),
  ).toBeVisible();
  expect(document.title).toBe("Home | Bloom");
});

it("navigates to the lazy about route and updates the page title", async () => {
  const user = userEvent.setup();
  renderApp();
  await screen.findByText(/Version 0\.1\.0-dev/);

  await user.click(screen.getByRole("link", { name: "About" }));

  expect(
    await screen.findByRole("heading", { name: "About Bloom", level: 1 }),
  ).toBeVisible();
  expect(document.title).toBe("About | Bloom");
  expect(screen.getByRole("link", { name: "About" })).toHaveAttribute(
    "aria-current",
    "page",
  );
});

it("loads the about route from a cold deep link", async () => {
  renderApp("/about");

  expect(
    await screen.findByRole("heading", { name: "About Bloom", level: 1 }),
  ).toBeVisible();
  expect(screen.getByRole("link", { name: "Return to home" })).toBeVisible();
});

it("returns to the home page from the about route", async () => {
  const user = userEvent.setup();
  renderApp("/about");
  await screen.findByRole("heading", { name: "About Bloom", level: 1 });

  await user.click(screen.getByRole("link", { name: "Return to home" }));

  expect(
    await screen.findByRole("heading", { name: "Bloom", level: 1 }),
  ).toBeVisible();
  expect(document.title).toBe("Home | Bloom");
});

it("renders the not-found route for an unknown path inside the layout", async () => {
  renderApp("/no-such-page");

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "The requested page does not exist",
  );
  expect(
    screen.getByRole("heading", { name: "Page not found", level: 1 }),
  ).toBeVisible();
  expect(
    screen.getByRole("navigation", { name: "Main navigation" }),
  ).toBeVisible();
  expect(document.title).toBe("Page not found | Bloom");
});
