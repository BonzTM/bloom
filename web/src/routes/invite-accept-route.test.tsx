import { expect, it } from "@jest/globals";
import type { QueryClient } from "@tanstack/react-query";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http } from "msw";
import {
  envelope,
  jsonApi,
  RATE_LIMITED_INVITE_CODE,
  REVOKED_INVITE_CODE,
  TAKEN_INVITE_USERNAME,
  VALID_INVITE_CODE,
} from "../mocks/handlers.js";
import { renderApp } from "../test/render-app.js";
import { server } from "../test/server.js";

const PASSWORD = "correct horse battery staple";

function cachedMutationText(queryClient: QueryClient): string {
  return JSON.stringify(
    queryClient
      .getMutationCache()
      .getAll()
      .map((mutation) => mutation.state),
  );
}

it("welcomes the invited person with the server's name and the account rules", async () => {
  renderApp(`/invite/${VALID_INVITE_CODE}`);
  expect(
    await screen.findByRole("heading", {
      name: "You are invited to Cabin",
      level: 1,
    }),
  ).toBeVisible();
  expect(document.title).toBe("Accept invite | Bloom");
  expect(screen.getByLabelText("Username")).toHaveAccessibleDescription(
    /letters, digits, spaces/,
  );
  expect(screen.getByLabelText("Password")).toHaveAccessibleDescription(
    /15 to 1024 characters/,
  );
});

it("creates the account, moves to a code-free address, and forgets the code and password", async () => {
  const user = userEvent.setup();
  const { queryClient, router } = renderApp(`/invite/${VALID_INVITE_CODE}`);
  await user.type(await screen.findByLabelText("Username"), "alice");
  await user.type(screen.getByLabelText("Password"), PASSWORD);

  await user.click(screen.getByRole("button", { name: "Create my account" }));

  expect(
    await screen.findByRole("heading", {
      name: "Your account is ready",
      level: 1,
    }),
  ).toBeVisible();
  expect(screen.getByText(/Sign in to Cabin as/)).toHaveTextContent(
    "Sign in to Cabin as alice with the password you chose.",
  );
  expect(router.state.location.pathname).toBe("/invite/accepted");
  expect(document.title).toBe("Account ready | Bloom");
  expect(cachedMutationText(queryClient)).not.toContain(PASSWORD);
  expect(cachedMutationText(queryClient)).not.toContain(VALID_INVITE_CODE);
  // The preview is keyed by the code and is collected once nothing shows it.
  await waitFor(() => {
    expect(
      JSON.stringify(
        queryClient
          .getQueryCache()
          .getAll()
          .map((q) => q.queryKey),
      ),
    ).not.toContain(VALID_INVITE_CODE);
  });
  expect(screen.queryByLabelText("Password")).not.toBeInTheDocument();
});

it("answers a reload of the confirmation without the details", async () => {
  renderApp("/invite/accepted");
  expect(
    await screen.findByRole("heading", {
      name: "Your account is ready",
      level: 1,
    }),
  ).toBeVisible();
  expect(screen.getByText(/Sign in to your media server/)).toBeVisible();
});

it("treats an invite that became unavailable during acceptance like any other", async () => {
  const user = userEvent.setup();
  renderApp(`/invite/${VALID_INVITE_CODE}`);
  await user.type(await screen.findByLabelText("Username"), "alice");
  await user.type(screen.getByLabelText("Password"), PASSWORD);
  server.use(
    http.post(
      "*/api/v1/invite/:code/accept",
      jsonApi(() => envelope(404, "not_found", "invite not available")),
    ),
  );

  await user.click(screen.getByRole("button", { name: "Create my account" }));

  const heading = await screen.findByRole("heading", {
    name: "Invite not available",
    level: 1,
  });
  expect(heading).toBeVisible();
  expect(heading).toHaveFocus();
  expect(screen.queryByLabelText("Password")).not.toBeInTheDocument();
});

it("explains a taken username and keeps what was typed", async () => {
  const user = userEvent.setup();
  renderApp(`/invite/${VALID_INVITE_CODE}`);
  await user.type(
    await screen.findByLabelText("Username"),
    TAKEN_INVITE_USERNAME,
  );
  await user.type(screen.getByLabelText("Password"), PASSWORD);

  await user.click(screen.getByRole("button", { name: "Create my account" }));

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "That username is already taken on the server. Choose another.",
  );
  expect(screen.getByRole("alert")).toHaveFocus();
  expect(screen.getByLabelText("Password")).toHaveValue(PASSWORD);
});

it("checks the password length before sending anything", async () => {
  const user = userEvent.setup();
  renderApp(`/invite/${VALID_INVITE_CODE}`);
  await user.type(await screen.findByLabelText("Username"), "alice");
  await user.type(screen.getByLabelText("Password"), "short");

  await user.click(screen.getByRole("button", { name: "Create my account" }));

  const password = screen.getByLabelText("Password");
  expect(password).toBeInvalid();
  expect(password).toHaveFocus();
  expect(password).toHaveAccessibleDescription(/Use at least 15 characters/);
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

it.each([
  ["a revoked invite", REVOKED_INVITE_CODE],
  ["a malformed code", "not-a-code"],
])("tells the visitor that %s is not available", async (_label, code) => {
  renderApp(`/invite/${code}`);
  expect(
    await screen.findByRole("heading", {
      name: "Invite not available",
      level: 1,
    }),
  ).toBeVisible();
  expect(screen.getByRole("alert")).toHaveTextContent(
    "This invite link is not valid, has expired, or has already been used.",
  );
  expect(screen.queryByLabelText("Username")).not.toBeInTheDocument();
});

it("asks a rate-limited visitor to wait and offers a retry", async () => {
  renderApp(`/invite/${RATE_LIMITED_INVITE_CODE}`);
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Too many attempts. Try again in 30 seconds.",
  );
  expect(screen.getByRole("button", { name: "Retry" })).toBeVisible();
});
