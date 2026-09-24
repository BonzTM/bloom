import { expect, it } from "@jest/globals";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import {
  envelope,
  jsonApi,
  mockRequests,
  setMockPermissions,
  signInMockSession,
} from "../mocks/handlers.js";
import { renderApp, type AppRender } from "../test/render-app.js";
import { server } from "../test/server.js";

// Changing the filter replaces the table, so it is found again afterwards.
function findTable(): Promise<HTMLElement> {
  return screen.findByRole("table", { name: "Requests, newest first" });
}

async function openRequests(): Promise<AppRender & { table: HTMLElement }> {
  signInMockSession();
  const rendered = renderApp("/admin/requests");
  const table = await screen.findByRole("table", {
    name: "Requests, newest first",
  });
  return { ...rendered, table };
}

it("offers the requests page from the administration index", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/admin");
  await user.click(await screen.findByRole("link", { name: "Requests" }));
  expect(
    await screen.findByRole("heading", { name: "Requests", level: 1 }),
  ).toBeVisible();
  expect(document.title).toBe("Requests | Bloom");
});

it("hides the requests link without requests.approve", async () => {
  setMockPermissions(["admin.settings"]);
  signInMockSession();
  renderApp("/admin");
  expect(
    await screen.findByRole("link", { name: "Request settings" }),
  ).toBeVisible();
  expect(
    screen.queryByRole("link", { name: "Requests" }),
  ).not.toBeInTheDocument();
});

it("shows pending requests first, with seasons, profile, and requester", async () => {
  const { table } = await openRequests();
  expect(screen.getByLabelText("Status")).toHaveValue("pending");
  const arrival = within(table).getByRole("row", {
    name: /^The Arrival \(2021\)/,
  });
  expect(arrival).toHaveTextContent("Seasons 1 and 2");
  expect(arrival).toHaveTextContent("Series");
  expect(arrival).toHaveTextContent("Series");
  expect(arrival).toHaveTextContent("bob");
  expect(arrival).toHaveTextContent("Pending");
  expect(arrival).toHaveTextContent("2026-09-23");
  expect(
    within(arrival).getByRole("button", { name: "Approve The Arrival (2021)" }),
  ).toBeVisible();
  const heat = within(table).getByRole("row", { name: /^Heat \(1995\)/ });
  expect(heat).toHaveTextContent("Movies HD");
  expect(
    within(table).queryByRole("row", { name: /^The Prestige/ }),
  ).not.toBeInTheDocument();
});

it("filters by status and shows the decision made", async () => {
  const user = userEvent.setup();
  await openRequests();

  await user.selectOptions(screen.getByLabelText("Status"), "Declined");
  const declined = await findTable();

  const prestige = await within(declined).findByRole("row", {
    name: /^The Prestige \(2006\)/,
  });
  expect(prestige).toHaveTextContent("Declined");
  expect(prestige).toHaveTextContent("2026-09-21");
  expect(prestige).toHaveTextContent("Already on the shelf.");
  expect(within(prestige).queryByRole("button")).not.toBeInTheDocument();
  expect(
    within(declined).queryByRole("row", { name: /^Heat/ }),
  ).not.toBeInTheDocument();

  await user.selectOptions(screen.getByLabelText("Status"), "All statuses");
  const all = await findTable();
  expect(await within(all).findByRole("row", { name: /^Heat/ })).toBeVisible();
  expect(within(all).getAllByRole("rowheader")).toHaveLength(6);
});

it("approves a request with a reason after confirming", async () => {
  const user = userEvent.setup();
  const { table } = await openRequests();

  await user.click(
    within(table).getByRole("button", { name: "Approve Heat (1995)" }),
  );
  const form = within(table).getByRole("form", {
    name: "Confirm approving Heat (1995)",
  });
  const reason = within(form).getByLabelText("Reason (optional)");
  expect(reason).toHaveFocus();
  await user.type(reason, "Classic.");
  await user.click(
    within(form).getByRole("button", { name: "Confirm approving Heat (1995)" }),
  );

  await within(table).findByRole("row", { name: /^The Arrival/ });
  expect(
    within(table).queryByRole("row", { name: /^Heat/ }),
  ).not.toBeInTheDocument();
  await user.selectOptions(screen.getByLabelText("Status"), "Approved");
  const decided = await findTable();
  const heat = await within(decided).findByRole("row", {
    name: /^Heat \(1995\)/,
  });
  expect(heat).toHaveTextContent("Approved");
  expect(heat).toHaveTextContent("Classic.");
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

it("returns focus to the decline button when cancelled", async () => {
  const user = userEvent.setup();
  const { table } = await openRequests();

  await user.click(
    within(table).getByRole("button", { name: "Decline Heat (1995)" }),
  );
  await user.click(
    within(table).getByRole("button", { name: "Cancel declining Heat (1995)" }),
  );

  expect(
    within(table).getByRole("button", { name: "Decline Heat (1995)" }),
  ).toHaveFocus();
});

it("declines a request without a reason", async () => {
  const user = userEvent.setup();
  const { table } = await openRequests();

  await user.click(
    within(table).getByRole("button", { name: "Decline Heat (1995)" }),
  );
  await user.click(
    within(table).getByRole("button", {
      name: "Confirm declining Heat (1995)",
    }),
  );

  await user.selectOptions(screen.getByLabelText("Status"), "Declined");
  const decided = await findTable();
  const heat = await within(decided).findByRole("row", {
    name: /^Heat \(1995\)/,
  });
  expect(heat).toHaveTextContent("Declined");
});

it("refuses a reason that is too long before sending it", async () => {
  const user = userEvent.setup();
  const { table } = await openRequests();
  await user.click(
    within(table).getByRole("button", { name: "Decline Heat (1995)" }),
  );
  const reason = within(table).getByLabelText("Reason (optional)");
  await user.click(reason);
  await user.paste("x".repeat(1001));

  await user.click(
    within(table).getByRole("button", {
      name: "Confirm declining Heat (1995)",
    }),
  );

  expect(reason).toBeInvalid();
  expect(reason).toHaveFocus();
  expect(reason).toHaveAccessibleDescription(
    "Use at most 1000 bytes for the reason.",
  );
});

it("focuses the explanation when a decision is refused", async () => {
  const user = userEvent.setup();
  const { table } = await openRequests();
  server.use(
    http.post(
      "*/api/v1/requests/:id/approve",
      jsonApi(() => envelope(409, "already_exists", "already decided")),
    ),
  );
  await user.click(
    within(table).getByRole("button", { name: "Approve Heat (1995)" }),
  );
  await user.click(
    within(table).getByRole("button", {
      name: "Confirm approving Heat (1995)",
    }),
  );

  const alert = await screen.findByRole("alert");
  expect(alert).toHaveTextContent(
    "That request cannot be changed from its current state.",
  );
  expect(alert).toHaveFocus();
});

it("works for an approver who cannot list profiles", async () => {
  setMockPermissions(["requests.approve"]);
  const { table } = await openRequests();
  expect(within(table).getByRole("row", { name: /^Heat/ })).toHaveTextContent(
    "Unknown profile",
  );
  expect(
    screen.queryByRole("link", { name: "Manage request settings." }),
  ).not.toBeInTheDocument();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

it("links to request settings for an administrator", async () => {
  await openRequests();
  expect(
    screen.getByRole("link", { name: "Manage request settings." }),
  ).toHaveAttribute("href", "/admin/request-profiles");
});

it("offers a retry when the requests cannot be listed", async () => {
  const user = userEvent.setup();
  let failing = true;
  server.use(
    http.get(
      "*/api/v1/requests",
      jsonApi(() => (failing ? envelope(500, "internal", "boom") : undefined)),
    ),
  );
  signInMockSession();
  renderApp("/admin/requests");
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "The requests could not be loaded.",
  );
  failing = false;
  await user.click(screen.getByRole("button", { name: "Retry" }));
  expect(
    await screen.findByRole("table", { name: "Requests, newest first" }),
  ).toBeVisible();
});

it("loads a later page on demand", async () => {
  const user = userEvent.setup();
  server.use(
    http.get(
      "*/api/v1/requests",
      jsonApi(({ request }) =>
        new URL(request.url).searchParams.has("cursor")
          ? undefined
          : HttpResponse.json({
              items: [mockRequests[0]],
              next_cursor: "offset:1",
            }),
      ),
    ),
  );
  const { table } = await openRequests();
  expect(within(table).getAllByRole("rowheader")).toHaveLength(1);

  await user.click(screen.getByRole("button", { name: "Load more requests" }));

  expect(await within(table).findAllByRole("rowheader")).toHaveLength(2);
  expect(within(table).getByRole("row", { name: /^Heat/ })).toBeVisible();
});

it("shows a failed request's reason and lets an approver retry it", async () => {
  const user = userEvent.setup();
  await openRequests();
  await user.selectOptions(screen.getByLabelText("Status"), "Failed");
  const table = await findTable();
  const row = await within(table).findByRole("row", {
    name: /^Pulp Fiction \(1994\)/,
  });
  expect(row).toHaveTextContent("Failed");
  expect(row).toHaveTextContent("root folder missing");

  await user.click(
    within(row).getByRole("button", {
      name: "Approve again Pulp Fiction (1994)",
    }),
  );

  await user.selectOptions(screen.getByLabelText("Status"), "Approved");
  const approved = await findTable();
  expect(
    await within(approved).findByRole("row", { name: /^Pulp Fiction/ }),
  ).toHaveTextContent("Approved");
});

it("reads queue progress for a processing request on demand", async () => {
  const user = userEvent.setup();
  let reads = 0;
  server.use(
    http.get(
      "*/api/v1/requests/:id/progress",
      jsonApi(() => {
        reads += 1;
        return undefined;
      }),
    ),
  );
  await openRequests();
  await user.selectOptions(screen.getByLabelText("Status"), "Processing");
  const table = await findTable();
  const row = await within(table).findByRole("row", {
    name: /^The Matrix \(1999\)/,
  });
  expect(reads).toBe(0);

  await user.click(
    within(row).getByRole("button", { name: "Progress of The Matrix (1999)" }),
  );

  expect(
    await within(row).findByLabelText("Progress of The Matrix (1999)"),
  ).toHaveTextContent("downloading, 75% done, expected 2026-09-23 13:30");
  // Strict Mode's double mount may cancel and repeat the first read; the
  // point is that nothing was read before the click.
  expect(reads).toBeGreaterThanOrEqual(1);
});
