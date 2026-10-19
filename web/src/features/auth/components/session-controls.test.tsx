import { expect, it } from "@jest/globals";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import {
  envelope,
  signInMockSession,
  jsonApi,
  mockSession,
} from "../../../mocks/handlers.js";
import { renderApp } from "../../../test/render-app.js";
import { openAccountMenu } from "../../../test/account-menu.js";
import { createGate } from "../../../test/gate.js";
import { server } from "../../../test/server.js";
import { authKeys } from "../hooks/auth-queries.js";

it("offers a retry when the session cannot be checked", async () => {
  const user = userEvent.setup();
  // A flag rather than a call counter: Strict Mode issues and cancels a first
  // fetch, so counting requests would make the test depend on that detail.
  let failing = true;
  server.use(
    http.get(
      "*/api/v1/auth/me",
      jsonApi(() =>
        failing
          ? new HttpResponse("upstream down", { status: 502 })
          : envelope(401, "unauthorized", "sign in required"),
      ),
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
    http.get(
      "*/api/v1/auth/me",
      jsonApi(async () => {
        if (failing) {
          return new HttpResponse("upstream down", { status: 502 });
        }
        await gate.wait;
        return envelope(401, "unauthorized", "sign in required");
      }),
    ),
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
      jsonApi(() => new HttpResponse("upstream down", { status: 502 })),
    ),
  );
  renderApp();

  await openAccountMenu(user);
  await user.click(screen.getByRole("button", { name: "Sign out" }));

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
    http.post(
      "*/api/v1/auth/logout",
      jsonApi(() => envelope(401, "unauthorized", "sign in required")),
    ),
  );
  renderApp();

  await openAccountMenu(user);
  await user.click(screen.getByRole("button", { name: "Sign out" }));

  expect(await screen.findByRole("link", { name: "Sign in" })).toBeVisible();
});

it("announces sign-out while it is pending and settles cleanly", async () => {
  const user = userEvent.setup();
  signInMockSession();
  const gate = createGate();
  server.use(
    http.post(
      "*/api/v1/auth/logout",
      jsonApi(async () => {
        await gate.wait;
        return new HttpResponse(null, { status: 204 });
      }),
    ),
  );
  renderApp();

  await openAccountMenu(user);
  // The live region must already exist, empty, before the message arrives,
  // or a screen reader may not announce it.
  const before = within(screen.getByRole("banner")).getAllByRole("status");
  await user.click(screen.getByRole("button", { name: "Sign out" }));

  expect(
    await screen.findByRole("button", { name: "Signing out…" }),
  ).toBeDisabled();
  const message = screen.getByText("Signing out, please wait.");
  expect(message).toHaveRole("status");
  expect(before).toContain(message);
  gate.open();
  expect(await screen.findByRole("link", { name: "Sign in" })).toBeVisible();
});

it("keeps showing the last known account when a refresh fails", async () => {
  const user = userEvent.setup();
  signInMockSession();
  let failing = false;
  server.use(
    http.get(
      "*/api/v1/auth/me",
      jsonApi(() =>
        failing
          ? new HttpResponse("upstream down", { status: 502 })
          : HttpResponse.json(mockSession),
      ),
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
  await openAccountMenu(user);
  expect(screen.getByRole("button", { name: "Sign out" })).toBeEnabled();
});

it("warns when a refresh fails while signed out and keeps the sign-in link", async () => {
  const user = userEvent.setup();
  let failing = false;
  server.use(
    http.get(
      "*/api/v1/auth/me",
      jsonApi(() =>
        failing
          ? new HttpResponse("upstream down", { status: 502 })
          : envelope(401, "unauthorized", "sign in required"),
      ),
    ),
  );
  const { queryClient } = renderApp();
  await screen.findByRole("link", { name: "Sign in" });

  failing = true;
  await queryClient.refetchQueries({ queryKey: authKeys.session() });

  await waitFor(() => {
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Sign-in status could not be refreshed.",
    );
  });
  expect(screen.getByRole("link", { name: "Sign in" })).toBeVisible();
  failing = false;
  await user.click(screen.getByRole("button", { name: "Retry" }));
  await waitFor(() => {
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});

it.each([
  ["signed in", () => HttpResponse.json(mockSession), "Signed in as admin"],
  [
    "signed out",
    () => envelope(401, "unauthorized", "sign in required"),
    "Sign in",
  ],
])(
  "disables and announces the retry of a failed refresh while %s",
  async (_label, healthy, visibleText) => {
    const user = userEvent.setup();
    const gate = createGate();
    let mode: "healthy" | "failing" | "held" = "healthy";
    server.use(
      http.get(
        "*/api/v1/auth/me",
        jsonApi(async () => {
          if (mode === "failing") {
            return new HttpResponse("upstream down", { status: 502 });
          }
          if (mode === "held") {
            await gate.wait;
          }
          return healthy();
        }),
      ),
    );
    const { queryClient } = renderApp();
    await screen.findByText(visibleText);

    // The home page's own panels retry their own requests; the session
    // controls live in the top bar.
    const bar = within(screen.getByRole("banner"));
    mode = "failing";
    await queryClient.refetchQueries({ queryKey: authKeys.session() });
    await bar.findByRole("button", { name: "Retry" });

    mode = "held";
    await user.click(bar.getByRole("button", { name: "Retry" }));

    expect(
      await bar.findByRole("button", { name: "Retrying…" }),
    ).toBeDisabled();
    expect(bar.getByText("Checking sign-in again.")).toHaveRole("status");
    expect(screen.getByText(visibleText)).toBeVisible();
    gate.open();
    await waitFor(() => {
      expect(bar.queryByRole("alert")).not.toBeInTheDocument();
    });
  },
);

it("closes the account menu on Escape and returns focus to its button", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp();
  await openAccountMenu(user);
  const button = screen.getByRole("button", { name: "Signed in as admin" });
  expect(button).toHaveAttribute("aria-expanded", "true");
  expect(screen.getByRole("button", { name: "Sign out" })).toBeVisible();

  await user.keyboard("{Escape}");

  expect(button).toHaveAttribute("aria-expanded", "false");
  expect(button).toHaveFocus();
  expect(
    screen.queryByRole("button", { name: "Sign out" }),
  ).not.toBeInTheDocument();
});

it("closes the account menu on a click elsewhere and on a new page", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp();
  await openAccountMenu(user);
  await user.click(screen.getByRole("main"));
  expect(
    screen.getByRole("button", { name: "Signed in as admin" }),
  ).toHaveAttribute("aria-expanded", "false");

  await openAccountMenu(user);
  await user.click(screen.getByRole("link", { name: "My statistics" }));
  expect(
    await screen.findByRole("heading", { name: "My statistics", level: 1 }),
  ).toBeVisible();
  expect(
    screen.getByRole("button", { name: "Signed in as admin" }),
  ).toHaveAttribute("aria-expanded", "false");
});

it("moves focus into the drawer and returns it to the menu button on Escape", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp();
  const toggle = await screen.findByRole("button", { name: "Menu" });

  await user.click(toggle);

  expect(toggle).toHaveAttribute("aria-expanded", "true");
  expect(
    within(
      screen.getByRole("navigation", { name: "Main navigation" }),
    ).getByRole("link", { name: "Home" }),
  ).toHaveFocus();
  await user.keyboard("{Escape}");
  expect(toggle).toHaveAttribute("aria-expanded", "false");
  expect(toggle).toHaveFocus();
});

it("closes the account menu when a link leads to the page already open", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/statistics");
  await screen.findByRole("heading", { name: "My statistics", level: 1 });
  await openAccountMenu(user);

  await user.click(
    within(screen.getByRole("banner")).getByRole("link", {
      name: "My statistics",
    }),
  );

  expect(
    screen.getByRole("button", { name: "Signed in as admin" }),
  ).toHaveAttribute("aria-expanded", "false");
});

it("closes the drawer when its link leads to the page already open", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/about");
  await screen.findByRole("heading", { name: "About Bloom", level: 1 });
  const toggle = screen.getByRole("button", { name: "Menu" });
  await user.click(toggle);

  await user.click(
    within(
      screen.getByRole("navigation", { name: "Main navigation" }),
    ).getByRole("link", { name: "About" }),
  );

  expect(toggle).toHaveAttribute("aria-expanded", "false");
});
