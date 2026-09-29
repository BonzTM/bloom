import { expect, it } from "@jest/globals";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setMockPermissions, signInMockSession } from "../mocks/handlers.js";
import { renderApp } from "../test/render-app.js";
import { server } from "../test/server.js";

async function openPreferences() {
  signInMockSession();
  const rendered = renderApp("/notifications");
  const form = await screen.findByRole("form", {
    name: "Notification preferences",
  });
  return { ...rendered, form };
}

it("reaches the page from the navigation with notifications.manage.own", async () => {
  const user = userEvent.setup();
  setMockPermissions(["notifications.manage.own"]);
  signInMockSession();
  renderApp();
  await user.click(
    await screen.findByRole("link", { name: "My notifications" }),
  );
  expect(
    await screen.findByRole("heading", { name: "My notifications", level: 1 }),
  ).toBeVisible();
  expect(document.title).toBe("My notifications | Bloom");
});

it("hides the page and the control without notifications.manage.own", async () => {
  setMockPermissions(["requests.create"]);
  signInMockSession();
  renderApp("/notifications");
  expect(
    await screen.findByRole("heading", { name: "Access denied", level: 1 }),
  ).toBeVisible();
  expect(
    screen.queryByRole("link", { name: "My notifications" }),
  ).not.toBeInTheDocument();
});

it("shows every event kind on and saves the whole matrix", async () => {
  const user = userEvent.setup();
  let sent: unknown;
  server.use(
    http.put("*/api/v1/me/notification-preferences", async ({ request }) => {
      const body: unknown = await request.clone().json();
      sent = body;
      return HttpResponse.json(JSON.parse(JSON.stringify(body)) as object);
    }),
  );
  const { form } = await openPreferences();
  const boxes = within(form).getAllByRole("checkbox");
  expect(boxes).toHaveLength(7);
  for (const box of boxes) {
    expect(box).toBeChecked();
  }

  await user.click(
    within(form).getByRole("checkbox", { name: /Playback started/ }),
  );
  await user.click(
    within(form).getByRole("button", { name: "Save preferences" }),
  );

  expect(await screen.findByText("Preferences saved.")).toHaveRole("status");
  expect(sent).toEqual([
    { event_type: "created", enabled: true },
    { event_type: "approved", enabled: true },
    { event_type: "declined", enabled: true },
    { event_type: "dispatched", enabled: true },
    { event_type: "available", enabled: true },
    { event_type: "failed", enabled: true },
    { event_type: "playback.session_started", enabled: false },
  ]);
});

it("keeps a saved choice on the next visit", async () => {
  const user = userEvent.setup();
  const { form } = await openPreferences();
  await user.click(
    within(form).getByRole("checkbox", { name: /Request made/ }),
  );
  await user.click(
    within(form).getByRole("button", { name: "Save preferences" }),
  );
  await screen.findByText("Preferences saved.");

  await user.click(screen.getByRole("link", { name: "Home" }));
  await user.click(screen.getByRole("link", { name: "My notifications" }));
  const again = await screen.findByRole("form", {
    name: "Notification preferences",
  });
  expect(
    within(again).getByRole("checkbox", { name: /Request made/ }),
  ).not.toBeChecked();
});

it("explains a failed save and keeps the draft", async () => {
  const user = userEvent.setup();
  server.use(
    http.put(
      "*/api/v1/me/notification-preferences",
      () => new HttpResponse("upstream down", { status: 502 }),
    ),
  );
  const { form } = await openPreferences();
  await user.click(
    within(form).getByRole("checkbox", { name: /Request made/ }),
  );
  await user.click(
    within(form).getByRole("button", { name: "Save preferences" }),
  );
  expect(
    await screen.findByText(
      "Your preferences could not be saved. Please try again.",
    ),
  ).toHaveRole("alert");
  expect(
    within(form).getByRole("checkbox", { name: /Request made/ }),
  ).not.toBeChecked();
});

it("follows and unfollows a title from its page", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/requests/movies/949");
  const follow = await screen.findByRole("button", {
    name: "Notify me when available about Heat (1995)",
  });
  expect(follow).toHaveAttribute("aria-pressed", "false");
  await user.click(follow);
  const stop = await screen.findByRole("button", {
    name: "Stop notifying me about Heat (1995)",
  });
  expect(stop).toHaveAttribute("aria-pressed", "true");
  expect(screen.getByText("You will be told when it is available.")).toHaveRole(
    "status",
  );
  await user.click(stop);
  expect(
    await screen.findByRole("button", {
      name: "Notify me when available about Heat (1995)",
    }),
  ).toBeVisible();
});
