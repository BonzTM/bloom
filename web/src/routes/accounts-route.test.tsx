import { expect, it } from "@jest/globals";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setMockPermissions, signInMockSession } from "../mocks/handlers.js";
import { renderApp } from "../test/render-app.js";

it("reaches the accounts page from the sidebar and lists accounts with their facts", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/admin");
  await user.click(
    await screen.findByRole("link", { name: "Administration: Accounts" }),
  );
  expect(
    await screen.findByRole("heading", { name: "Accounts", level: 1 }),
  ).toBeVisible();
  expect(document.title).toBe("Accounts | Bloom");
  const table = await screen.findByRole("table", { name: "Accounts" });
  const admin = within(table)
    .getByRole("link", { name: "admin" })
    .closest("tr");
  expect(admin).toHaveTextContent("You");
  expect(admin).toHaveTextContent("Password and single sign-on");
  expect(admin).toHaveTextContent("Cabin: josh");
  const alice = within(table)
    .getByRole("link", { name: "alice" })
    .closest("tr");
  expect(alice).toHaveTextContent("viewer (SSO), requester");
  expect(alice).toHaveTextContent("Single sign-on");
  expect(alice).toHaveTextContent("Cabin: alice (suppressed)");
  const carol = within(table)
    .getByRole("link", { name: "carol" })
    .closest("tr");
  expect(carol).toHaveTextContent("None");
});

it("loads the second page of accounts on demand", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/admin/accounts");
  const table = await screen.findByRole("table", { name: "Accounts" });
  expect(within(table).getAllByRole("row")).toHaveLength(51);
  await user.click(screen.getByRole("button", { name: "Load more accounts" }));
  await waitFor(() => {
    expect(within(table).getAllByRole("row")).toHaveLength(56);
  });
  expect(
    screen.queryByRole("button", { name: "Load more accounts" }),
  ).not.toBeInTheDocument();
});

it("searches by username through the URL", async () => {
  const user = userEvent.setup();
  signInMockSession();
  const app = renderApp("/admin/accounts");
  const form = await screen.findByRole("search", { name: "Search accounts" });
  await user.type(within(form).getByLabelText("Username contains"), "ali");
  await user.click(within(form).getByRole("button", { name: "Search" }));
  const table = await screen.findByRole("table", {
    name: 'Accounts matching "ali"',
  });
  expect(within(table).getAllByRole("row")).toHaveLength(2);
  expect(app.router.state.location.search).toBe("?q=ali");
  await user.clear(within(form).getByLabelText("Username contains"));
  await user.type(within(form).getByLabelText("Username contains"), "zzz");
  await user.click(within(form).getByRole("button", { name: "Search" }));
  expect(await screen.findByText('No account matches "zzz".')).toBeVisible();
});

it("shows one account and returns to the list", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/admin/accounts/7a000000-0000-4000-8000-0000000000a1");
  expect(
    await screen.findByRole("heading", { name: "alice", level: 1 }),
  ).toBeVisible();
  expect(document.title).toBe("alice | Bloom");
  expect(screen.getByText("viewer (SSO), requester")).toBeVisible();
  expect(screen.getByText("Cabin: alice (suppressed)")).toBeVisible();
  await user.click(screen.getByRole("link", { name: "All accounts" }));
  expect(
    await screen.findByRole("heading", { name: "Accounts", level: 1 }),
  ).toBeVisible();
});

it("treats an unknown or malformed account id as not found", async () => {
  signInMockSession();
  renderApp("/admin/accounts/7a000000-0000-4000-8000-0000000000ff");
  expect(
    await screen.findByRole("heading", { name: "Page not found" }),
  ).toBeVisible();
});

it("denies the pages without users.manage", async () => {
  setMockPermissions(["admin.roles"]);
  signInMockSession();
  renderApp("/admin/accounts");
  expect(
    await screen.findByRole("heading", { name: "Access denied" }),
  ).toBeVisible();
});
