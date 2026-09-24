import { expect, it } from "@jest/globals";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http } from "msw";
import {
  envelope,
  jsonApi,
  setMockPermissions,
  signInMockSession,
} from "../mocks/handlers.js";
import { renderApp } from "../test/render-app.js";
import { server } from "../test/server.js";

async function openStatistics(): Promise<void> {
  signInMockSession();
  renderApp("/admin/statistics");
  await screen.findByRole("heading", { name: "Statistics", level: 1 });
}

it("offers statistics from the home page and the administration index", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp();
  expect(
    await screen.findByRole("link", { name: "Open statistics" }),
  ).toHaveAttribute("href", "/admin/statistics");
  await user.click(await screen.findByRole("link", { name: "Admin" }));
  await user.click(await screen.findByRole("link", { name: "Statistics" }));
  expect(
    await screen.findByRole("heading", { name: "Statistics", level: 1 }),
  ).toBeVisible();
  expect(document.title).toBe("Statistics | Bloom");
});

it("refuses the page without stats.read.all", async () => {
  setMockPermissions(["admin.roles"]);
  signInMockSession();
  renderApp("/admin/statistics");
  expect(
    await screen.findByRole("heading", { name: "Access denied" }),
  ).toBeVisible();
});

it("shows totals, rankings, breakdowns, and charts for the window", async () => {
  await openStatistics();
  const totals = await screen.findByRole("list", { name: "Totals" });
  expect(totals).toHaveTextContent("Plays23");
  expect(totals).toHaveTextContent("Watch time29h 2m");
  expect(totals).toHaveTextContent("People2");
  expect(
    within(
      screen.getByRole("figure", { name: "Most watched movies" }),
    ).getByRole("row", { name: /^Ronin / }),
  ).toHaveTextContent("6");
  expect(
    within(
      screen.getByRole("figure", { name: "Most watched series" }),
    ).getByRole("row", { name: /^The Arrival / }),
  ).toHaveTextContent("14");
  expect(
    within(
      screen.getByRole("figure", { name: "Most active people" }),
    ).getByRole("row", { name: /^alice / }),
  ).toHaveTextContent("18h 2m");
  expect(
    within(screen.getByRole("figure", { name: "Play methods" })).getByRole(
      "row",
      { name: /^Direct play / },
    ),
  ).toHaveTextContent("20");
  expect(
    await screen.findByRole("figure", { name: "Plays per day" }),
  ).toBeVisible();
  expect(
    within(
      await screen.findByRole("figure", { name: "Plays by hour" }),
    ).getByRole("row", { name: /^21:00 / }),
  ).toHaveTextContent("12");
  expect(
    within(screen.getByRole("figure", { name: "Plays by weekday" })).getByRole(
      "row",
      { name: /^Sat / },
    ),
  ).toHaveTextContent("9");
});

it("links each active person to their own dashboard", async () => {
  await openStatistics();
  const people = await screen.findByRole("list", {
    name: "People with statistics",
  });
  expect(within(people).getByRole("link", { name: "alice" })).toHaveAttribute(
    "href",
    "/admin/statistics/users/3d7f1a2b-0000-4000-8000-000000000001/u-alice",
  );
});

it("recomputes for a different window and says which zone applies", async () => {
  const user = userEvent.setup();
  const requested: string[] = [];
  server.use(
    http.get(
      "*/api/v1/stats/daily",
      jsonApi(({ request }) => {
        requested.push(new URL(request.url).search);
        return undefined;
      }),
    ),
  );
  await openStatistics();
  await screen.findByRole("figure", { name: "Plays per day" });
  expect(screen.getByText(/Days and hours in /)).toBeVisible();

  await user.selectOptions(screen.getByLabelText("Window"), "Last 7 days");

  const daily = await screen.findByRole("figure", { name: "Plays per day" });
  expect(
    await within(daily).findAllByRole("row", { name: /^2026-09/ }),
  ).toHaveLength(7);
  expect(requested.some((q) => q.includes("days=7"))).toBe(true);
  expect(requested.every((q) => /tz=[^&]+/.test(q))).toBe(true);
});

it("offers a retry when one report fails", async () => {
  const user = userEvent.setup();
  let failing = true;
  server.use(
    http.get(
      "*/api/v1/stats/patterns",
      jsonApi(() => (failing ? envelope(500, "internal", "boom") : undefined)),
    ),
  );
  await openStatistics();
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Patterns could not be loaded.",
  );
  expect(await screen.findByRole("list", { name: "Totals" })).toBeVisible();
  failing = false;
  await user.click(screen.getByRole("button", { name: "Retry" }));
  expect(
    await screen.findByRole("figure", { name: "Plays by weekday" }),
  ).toBeVisible();
});

it("ranks libraries and groups watches without a known library", async () => {
  await openStatistics();
  const chart = await screen.findByRole("figure", {
    name: "Most watched libraries",
  });
  expect(
    within(chart).getByRole("row", { name: /^Movies / }),
  ).toHaveTextContent("12");
  expect(
    within(chart).getByRole("row", { name: /^Unknown library / }),
  ).toHaveTextContent("3");
});

it("filters every report by one library and clears it when the server changes", async () => {
  const user = userEvent.setup();
  const requested: string[] = [];
  server.use(
    http.get(
      "*/api/v1/stats/daily",
      jsonApi(({ request }) => {
        requested.push(new URL(request.url).search);
        return undefined;
      }),
    ),
  );
  await openStatistics();
  const library = await screen.findByLabelText("Library");
  expect(
    within(library).queryByRole("option", { name: "Unknown library" }),
  ).not.toBeInTheDocument();

  await user.selectOptions(library, "Shows");

  expect(await screen.findByLabelText("Server")).toHaveDisplayValue(
    "Living room",
  );
  await screen.findByRole("figure", { name: "Plays per day" });
  const filtered = requested.find((q) => q.includes("library_id=lib-shows"));
  expect(filtered).toContain(
    "media_server_id=3d7f1a2b-0000-4000-8000-000000000002",
  );

  await user.selectOptions(screen.getByLabelText("Server"), "All servers");

  expect(screen.getByLabelText("Library")).toHaveDisplayValue("All libraries");
  expect(screen.getByLabelText("Server")).toHaveDisplayValue("All servers");
});
