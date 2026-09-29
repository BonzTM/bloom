import { expect, it } from "@jest/globals";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setMockPermissions, signInMockSession } from "../mocks/handlers.js";
import { renderApp } from "../test/render-app.js";

async function openActivity(path = "/admin/activity") {
  signInMockSession();
  const rendered = renderApp(path);
  const table = await screen.findByRole("table", {
    name: "Watches matching the filters, newest first",
  });
  return { ...rendered, table };
}

it("lists every watch with each person linked to their statistics", async () => {
  const { table } = await openActivity();
  expect(document.title).toBe("Activity | Bloom");
  const rows = within(table).getAllByRole("row").slice(1);
  expect(rows.length).toBeGreaterThan(1);
  const alice = within(table).getAllByRole("link", { name: "alice" })[0];
  expect(alice).toHaveAttribute(
    "href",
    "/admin/statistics/users/3d7f1a2b-0000-4000-8000-000000000001/u-alice",
  );
  expect(
    within(table).getAllByRole("link", { name: /^Details of / })[0],
  ).toHaveAttribute(
    "href",
    expect.stringMatching(/^\/admin\/playback\/watches\//),
  );
});

it("filters by title through the URL and offers the servers seen", async () => {
  const user = userEvent.setup();
  const { table } = await openActivity();
  const before = within(table).getAllByRole("row").length;
  const form = screen.getByRole("form", { name: "Filter activity" });
  expect(
    within(form).getByRole("option", { name: "Cabin" }),
  ).toBeInTheDocument();

  await user.type(within(form).getByLabelText("Title contains"), "ronin");
  await user.click(within(form).getByRole("button", { name: "Apply filters" }));

  const filtered = await screen.findByRole("table", {
    name: "Watches matching the filters, newest first",
  });
  await within(filtered).findByText("Ronin");
  expect(within(filtered).getAllByRole("row").length).toBeLessThan(before);
  expect(within(filtered).queryByText("Fringe")).not.toBeInTheDocument();
  // The form re-reads the filter from the address, so the value stays.
  expect(
    within(
      screen.getByRole("form", { name: "Filter activity" }),
    ).getByLabelText("Title contains"),
  ).toHaveValue("ronin");
});

it("reads the filter from a shared address and drops a bad value", async () => {
  await openActivity("/admin/activity?method=direct_stream&server=not-a-uuid");
  const form = screen.getByRole("form", { name: "Filter activity" });
  expect(within(form).getByLabelText("Delivery")).toHaveValue("direct_stream");
  expect(within(form).getByLabelText("Server")).toHaveValue("");
});

it("says so when nothing matches", async () => {
  signInMockSession();
  renderApp("/admin/activity?q=zzzz-nothing");
  expect(await screen.findByText("Nothing matches these filters.")).toHaveRole(
    "status",
  );
});

it("refuses the page without stats.read.all", async () => {
  setMockPermissions(["stats.read.own"]);
  signInMockSession();
  renderApp("/admin/activity");
  expect(
    await screen.findByRole("heading", { name: "Access denied", level: 1 }),
  ).toBeVisible();
});
