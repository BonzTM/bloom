import { expect, it } from "@jest/globals";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { envelope, signInMockSession } from "../../../mocks/handlers.js";
import { renderApp } from "../../../test/render-app.js";
import { server } from "../../../test/server.js";
import { authKeys } from "../hooks/auth-queries.js";

function createGate() {
  let open = (): void => undefined;
  const wait = new Promise<void>((resolve) => {
    open = resolve;
  });
  return { wait, open };
}

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

it("announces the retry and withdraws the button while it runs", async () => {
  const user = userEvent.setup();
  const gate = createGate();
  let failing = true;
  server.use(
    http.get("*/api/v1/auth/me", async () => {
      if (failing) {
        return new HttpResponse("upstream down", { status: 502 });
      }
      await gate.wait;
      return envelope(401, "unauthenticated", "sign in required");
    }),
  );
  renderApp();
  await screen.findByRole("button", { name: "Retry" });

  failing = false;
  await user.click(screen.getByRole("button", { name: "Retry" }));

  expect(await screen.findByText("Checking sign-in…")).toHaveRole("status");
  expect(
    screen.queryByRole("button", { name: "Retry" }),
  ).not.toBeInTheDocument();
  gate.open();
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

it("announces sign-out while it is pending and settles cleanly", async () => {
  const user = userEvent.setup();
  signInMockSession();
  const gate = createGate();
  server.use(
    http.post("*/api/v1/auth/logout", async () => {
      await gate.wait;
      return new HttpResponse(null, { status: 204 });
    }),
  );
  renderApp();

  await user.click(await screen.findByRole("button", { name: "Sign out" }));

  expect(
    await screen.findByRole("button", { name: "Signing out…" }),
  ).toBeDisabled();
  expect(screen.getByText("Signing out, please wait.")).toHaveRole("status");
  gate.open();
  expect(await screen.findByRole("link", { name: "Sign in" })).toBeVisible();
});

it("keeps showing the last known account when a refresh fails", async () => {
  signInMockSession();
  let failing = false;
  server.use(
    http.get("*/api/v1/auth/me", () =>
      failing
        ? new HttpResponse("upstream down", { status: 502 })
        : HttpResponse.json({ account: { id: "1", username: "admin" } }),
    ),
  );
  const { queryClient } = renderApp();
  await screen.findByText("Signed in as admin");

  failing = true;
  await queryClient.refetchQueries({ queryKey: authKeys.session() });

  await waitFor(() => {
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Sign-in status could not be refreshed.",
    );
  });
  expect(screen.getByText("Signed in as admin")).toBeVisible();
  expect(screen.getByRole("button", { name: "Sign out" })).toBeEnabled();
});
