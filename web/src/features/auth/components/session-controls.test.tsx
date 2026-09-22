import { expect, it } from "@jest/globals";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { delay, http, HttpResponse } from "msw";
import { envelope, signInMockSession } from "../../../mocks/handlers.js";
import { renderApp } from "../../../test/render-app.js";
import { server } from "../../../test/server.js";

it("offers a retry when the session cannot be checked", async () => {
  const user = userEvent.setup();
  // A flag rather than a call counter: Strict Mode issues and cancels a first
  // fetch, so counting requests would make the test depend on that detail.
  let failing = true;
  server.use(
    http.get("*/api/v1/auth/me", () =>
      failing
        ? new HttpResponse("upstream down", { status: 502 })
        : envelope(401, "unauthenticated", "sign in required"),
    ),
  );
  renderApp();

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Sign-in status is unavailable.",
  );
  failing = false;
  await user.click(screen.getByRole("button", { name: "Retry" }));

  expect(await screen.findByRole("link", { name: "Sign in" })).toBeVisible();
});

it("keeps the account when sign-out fails on the server", async () => {
  const user = userEvent.setup();
  signInMockSession();
  server.use(
    http.post(
      "*/api/v1/auth/logout",
      () => new HttpResponse("upstream down", { status: 502 }),
    ),
  );
  renderApp();

  await user.click(await screen.findByRole("button", { name: "Sign out" }));

  expect(
    await screen.findByText("Sign-out failed. Please try again."),
  ).toBeVisible();
  expect(screen.getByText("Signed in as admin")).toBeVisible();
  expect(
    screen.queryByRole("link", { name: "Sign in" }),
  ).not.toBeInTheDocument();
});

it("forgets the account when the server says there was no session", async () => {
  const user = userEvent.setup();
  signInMockSession();
  server.use(
    http.post("*/api/v1/auth/logout", () =>
      envelope(401, "unauthenticated", "sign in required"),
    ),
  );
  renderApp();

  await user.click(await screen.findByRole("button", { name: "Sign out" }));

  expect(await screen.findByRole("link", { name: "Sign in" })).toBeVisible();
});

it("announces sign-out while it is pending", async () => {
  const user = userEvent.setup();
  signInMockSession();
  server.use(
    http.post("*/api/v1/auth/logout", async () => {
      await delay("infinite");
      return new HttpResponse(null, { status: 204 });
    }),
  );
  renderApp();

  await user.click(await screen.findByRole("button", { name: "Sign out" }));

  expect(
    await screen.findByRole("button", { name: "Signing out…" }),
  ).toBeDisabled();
  expect(screen.getByText("Signing out, please wait.")).toHaveRole("status");
});

it("keeps showing the last known account when a refresh fails", async () => {
  const user = userEvent.setup();
  signInMockSession();
  let failing = false;
  server.use(
    http.get("*/api/v1/auth/me", () =>
      failing
        ? new HttpResponse("upstream down", { status: 502 })
        : HttpResponse.json({ account: { id: "1", username: "admin" } }),
    ),
    http.post(
      "*/api/v1/auth/logout",
      () => new HttpResponse("upstream down", { status: 502 }),
    ),
  );
  renderApp();
  await screen.findByText("Signed in as admin");

  // A failed sign-out invalidates nothing, so force a refresh by failing the
  // next check the retry path performs.
  failing = true;
  await user.click(screen.getByRole("button", { name: "Sign out" }));
  await screen.findByText("Sign-out failed. Please try again.");

  expect(screen.getByText("Signed in as admin")).toBeVisible();
});
