import { expect, it } from "@jest/globals";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http } from "msw";
import { envelope, jsonApi, signInMockSession } from "../mocks/handlers.js";
import { renderApp } from "../test/render-app.js";
import { server } from "../test/server.js";

type User = ReturnType<typeof userEvent.setup>;

async function openQuota(user: User, role: string): Promise<HTMLElement> {
  signInMockSession();
  renderApp("/admin/roles");
  const table = await screen.findByRole("table", {
    name: "Roles, ordered by name",
  });
  const row = within(table).getByRole("row", { name: new RegExp(`^${role} `) });
  await user.click(
    within(row).getByRole("button", { name: `Quota for ${role}` }),
  );
  return within(row).findByRole("form", { name: `Request quota for ${role}` });
}

it("fetches nothing for a quota until it is opened", async () => {
  let reads = 0;
  server.use(
    http.get(
      "*/api/v1/roles/:id/request-quota",
      jsonApi(() => {
        reads += 1;
        return undefined;
      }),
    ),
  );
  signInMockSession();
  renderApp("/admin/roles");
  await screen.findByRole("table", { name: "Roles, ordered by name" });
  expect(reads).toBe(0);
});

it("shows the current quota and saves a change", async () => {
  const user = userEvent.setup();
  const form = await openQuota(user, "custom-01");
  expect(form).toHaveTextContent(
    "Current quota: 2 movies per 30 days, 4 seasons per 30 days.",
  );
  expect(within(form).getByLabelText("Movie limit")).toHaveValue("2");

  await user.clear(within(form).getByLabelText("Movie limit"));
  await user.type(within(form).getByLabelText("Movie limit"), "5");
  await user.click(within(form).getByRole("button", { name: "Save quota" }));

  expect(
    await within(form).findByText("Quota for custom-01 saved."),
  ).toBeVisible();
  expect(form).toHaveTextContent("Current quota: 5 movies per 30 days");
});

it("treats a role without a quota as unlimited and can set one", async () => {
  const user = userEvent.setup();
  const form = await openQuota(user, "admin");
  expect(form).toHaveTextContent("No quota: this role is not limited.");
  expect(
    within(form).queryByRole("button", { name: "Remove quota" }),
  ).not.toBeInTheDocument();

  await user.clear(within(form).getByLabelText("Season limit"));
  await user.type(within(form).getByLabelText("Season limit"), "3");
  const period = within(form).getByLabelText("Season period (days)");
  await user.clear(period);
  await user.type(period, "7");
  await user.click(within(form).getByRole("button", { name: "Save quota" }));

  expect(await within(form).findByText("Quota for admin saved.")).toBeVisible();
  expect(form).toHaveTextContent(
    "Current quota: movies unlimited, 3 seasons per 7 days.",
  );
  expect(
    within(form).getByRole("button", { name: "Remove quota" }),
  ).toBeVisible();
});

it("removes a quota", async () => {
  const user = userEvent.setup();
  const form = await openQuota(user, "custom-01");
  await user.click(within(form).getByRole("button", { name: "Remove quota" }));
  expect(
    await within(form).findByText("No quota: this role is not limited."),
  ).toBeVisible();
  expect(
    within(form).queryByRole("button", { name: "Remove quota" }),
  ).not.toBeInTheDocument();
});

it("refuses a period beyond ten years before sending anything", async () => {
  const user = userEvent.setup();
  let writes = 0;
  server.use(
    http.put(
      "*/api/v1/roles/:id/request-quota",
      jsonApi(() => {
        writes += 1;
        return envelope(500, "internal", "boom");
      }),
    ),
  );
  const form = await openQuota(user, "custom-01");
  const period = within(form).getByLabelText("Movie period (days)");
  await user.clear(period);
  await user.type(period, "4000");
  await user.click(within(form).getByRole("button", { name: "Save quota" }));

  expect(period).toBeInvalid();
  expect(period).toHaveAccessibleDescription(
    "Enter a whole number of days from 0 to 3650.",
  );
  expect(within(form).getByLabelText("Movie limit")).toHaveFocus();
  expect(writes).toBe(0);
});

it("explains a refused save and keeps the values", async () => {
  const user = userEvent.setup();
  server.use(
    http.put(
      "*/api/v1/roles/:id/request-quota",
      jsonApi(() => envelope(422, "validation_failed", "no")),
    ),
  );
  const form = await openQuota(user, "custom-01");
  await user.clear(within(form).getByLabelText("Movie limit"));
  await user.type(within(form).getByLabelText("Movie limit"), "9");
  await user.click(within(form).getByRole("button", { name: "Save quota" }));
  const alert = await within(form).findByRole("alert");
  expect(alert).toHaveTextContent("Check the quota values and try again.");
  expect(alert).toHaveFocus();
  expect(within(form).getByLabelText("Movie limit")).toHaveValue("9");
});

it("closes the editor and returns to the button", async () => {
  const user = userEvent.setup();
  const form = await openQuota(user, "custom-01");
  await user.click(
    within(form).getByRole("button", { name: "Close quota for custom-01" }),
  );
  expect(
    await screen.findByRole("button", { name: "Quota for custom-01" }),
  ).toBeVisible();
});
