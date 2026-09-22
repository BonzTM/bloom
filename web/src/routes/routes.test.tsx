import { expect, it } from "@jest/globals";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { mockAccount, signInMockSession, jsonApi } from "../mocks/handlers.js";
import { renderApp } from "../test/render-app.js";
import { createGate } from "../test/gate.js";
import { server } from "../test/server.js";

it("renders the home page with the server version from the API", async () => {
  renderApp();

  expect(
    screen.getByRole("heading", { name: "Bloom", level: 1 }),
  ).toBeVisible();
  expect(screen.getByText(/Loading server version/)).toHaveRole("status");
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

it("signs in from the login page and shows the account in the navigation", async () => {
  const user = userEvent.setup();
  renderApp("/login");

  expect(
    await screen.findByRole("heading", { name: "Sign in", level: 1 }),
  ).toBeVisible();
  expect(document.title).toBe("Sign in | Bloom");

  await user.type(screen.getByLabelText("Username"), "admin");
  await user.type(screen.getByLabelText("Password"), "correct horse");
  await user.click(screen.getByRole("button", { name: "Sign in" }));

  expect(
    await screen.findByRole("heading", { name: "Bloom", level: 1 }),
  ).toBeVisible();
  expect(screen.getByText("Signed in as admin")).toBeVisible();
  expect(
    screen.queryByRole("link", { name: "Sign in" }),
  ).not.toBeInTheDocument();
});

it("keeps the person on the login page after a rejected sign-in", async () => {
  const user = userEvent.setup();
  renderApp("/login");
  await screen.findByRole("heading", { name: "Sign in", level: 1 });

  await user.type(screen.getByLabelText("Username"), "admin");
  await user.type(screen.getByLabelText("Password"), "wrong");
  await user.click(screen.getByRole("button", { name: "Sign in" }));

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "The username or password is incorrect.",
  );
  expect(screen.getByLabelText("Username")).toHaveValue("admin");
  expect(document.title).toBe("Sign in | Bloom");
});

it("shows the retry delay when the server rate limits sign-in", async () => {
  const user = userEvent.setup();
  renderApp("/login");
  await screen.findByRole("heading", { name: "Sign in", level: 1 });

  await user.type(screen.getByLabelText("Username"), "locked");
  await user.type(screen.getByLabelText("Password"), "anything");
  await user.click(screen.getByRole("button", { name: "Sign in" }));

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Try again in 30 seconds.",
  );
});

it("signs out from the navigation and offers sign-in again", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp();

  await user.click(await screen.findByRole("button", { name: "Sign out" }));

  expect(await screen.findByRole("link", { name: "Sign in" })).toBeVisible();
  expect(screen.queryByText("Signed in as admin")).not.toBeInTheDocument();
});

it("sends an already signed-in visitor away from the login page", async () => {
  signInMockSession();
  renderApp("/login");

  expect(
    await screen.findByRole("heading", { name: "Bloom", level: 1 }),
  ).toBeVisible();
  expect(document.title).toBe("Home | Bloom");
});

it("returns to the page that asked for sign-in", async () => {
  const user = userEvent.setup();
  renderApp({ pathname: "/login", state: { from: "/about" } });
  await screen.findByRole("heading", { name: "Sign in", level: 1 });

  await user.type(screen.getByLabelText("Username"), "admin");
  await user.type(screen.getByLabelText("Password"), "correct horse");
  await user.click(screen.getByRole("button", { name: "Sign in" }));

  expect(
    await screen.findByRole("heading", { name: "About Bloom", level: 1 }),
  ).toBeVisible();
});

it("ignores a hostile return destination and goes home", async () => {
  const user = userEvent.setup();
  renderApp({ pathname: "/login", state: { from: "https://evil.example/" } });
  await screen.findByRole("heading", { name: "Sign in", level: 1 });

  await user.type(screen.getByLabelText("Username"), "admin");
  await user.type(screen.getByLabelText("Password"), "correct horse");
  await user.click(screen.getByRole("button", { name: "Sign in" }));

  expect(
    await screen.findByRole("heading", { name: "Bloom", level: 1 }),
  ).toBeVisible();
  expect(document.title).toBe("Home | Bloom");
});

it("treats a malformed sign-in response as a failure, not a session", async () => {
  const user = userEvent.setup();
  server.use(
    http.post(
      "*/api/v1/auth/login",
      jsonApi(() => HttpResponse.json({ account: { id: mockAccount.id } })),
    ),
  );
  renderApp("/login");
  await screen.findByRole("heading", { name: "Sign in", level: 1 });

  await user.type(screen.getByLabelText("Username"), "admin");
  await user.type(screen.getByLabelText("Password"), "correct horse");
  await user.click(screen.getByRole("button", { name: "Sign in" }));

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Sign-in failed. Please try again.",
  );
  expect(document.title).toBe("Sign in | Bloom");
});

it("moves focus to the main region after navigation", async () => {
  const user = userEvent.setup();
  renderApp();
  await screen.findByText(/Version 0\.1\.0-dev/);

  await user.click(screen.getByRole("link", { name: "About" }));
  await screen.findByRole("heading", { name: "About Bloom", level: 1 });

  expect(screen.getByRole("main")).toHaveFocus();
});

it("brings the person back to the page where they chose to sign in", async () => {
  const user = userEvent.setup();
  renderApp("/about");
  await screen.findByRole("heading", { name: "About Bloom", level: 1 });

  await user.click(await screen.findByRole("link", { name: "Sign in" }));
  await screen.findByRole("heading", { name: "Sign in", level: 1 });
  await user.type(screen.getByLabelText("Username"), "admin");
  await user.type(screen.getByLabelText("Password"), "correct horse");
  await user.click(screen.getByRole("button", { name: "Sign in" }));

  expect(
    await screen.findByRole("heading", { name: "About Bloom", level: 1 }),
  ).toBeVisible();
  expect(screen.getByText("Signed in as admin")).toBeVisible();
});

it("does not redirect while a sign-in is still pending, then redirects exactly once", async () => {
  const user = userEvent.setup();
  const gate = createGate();
  server.use(
    http.post(
      "*/api/v1/auth/login",
      jsonApi(async () => {
        await gate.wait;
        signInMockSession();
        return HttpResponse.json({ account: mockAccount });
      }),
    ),
  );
  const { queryClient, router } = renderApp("/login");
  await screen.findByRole("heading", { name: "Sign in", level: 1 });
  let arrivals = 0;
  let previous = router.state.location.key;
  const unsubscribe = router.subscribe((state) => {
    if (state.location.key !== previous) {
      previous = state.location.key;
      if (state.location.pathname === "/") {
        arrivals += 1;
      }
    }
  });

  await user.type(screen.getByLabelText("Username"), "admin");
  await user.type(screen.getByLabelText("Password"), "correct horse");
  await user.click(screen.getByRole("button", { name: "Sign in" }));
  await screen.findByRole("button", { name: "Signing in…" });

  // A stale cached account must not eject the person mid sign-in.
  queryClient.setQueryData(["auth", "session"], mockAccount);
  expect(
    screen.getByRole("heading", { name: "Sign in", level: 1 }),
  ).toBeVisible();

  gate.open();
  expect(
    await screen.findByRole("heading", { name: "Bloom", level: 1 }),
  ).toBeVisible();
  expect(document.title).toBe("Home | Bloom");
  expect(arrivals).toBe(1);
  unsubscribe();
});

it("lets the person correct a rejected password and sign in with Enter", async () => {
  const user = userEvent.setup();
  renderApp("/login");
  await screen.findByRole("heading", { name: "Sign in", level: 1 });

  await user.type(screen.getByLabelText("Username"), "admin");
  await user.type(screen.getByLabelText("Password"), "wrong{Enter}");
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "The username or password is incorrect.",
  );

  await user.clear(screen.getByLabelText("Password"));
  await user.type(screen.getByLabelText("Password"), "correct horse{Enter}");

  expect(
    await screen.findByRole("heading", { name: "Bloom", level: 1 }),
  ).toBeVisible();
  expect(screen.getByText("Signed in as admin")).toBeVisible();
});
