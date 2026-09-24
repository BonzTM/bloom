import { expect, it } from "@jest/globals";
import type { QueryClient } from "@tanstack/react-query";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import {
  CHANNEL_WEBHOOK_ID,
  envelope,
  jsonApi,
  mockNotificationChannels,
  setMockPermissions,
  signInMockSession,
  UNREACHABLE_CHANNEL_HOST,
} from "../mocks/handlers.js";
import { renderApp, type AppRender } from "../test/render-app.js";
import { server } from "../test/server.js";

type User = ReturnType<typeof userEvent.setup>;

async function openChannels(): Promise<AppRender & { table: HTMLElement }> {
  signInMockSession();
  const rendered = renderApp("/admin/notifications");
  const table = await screen.findByRole("table", {
    name: "Notification channels, ordered by name",
  });
  return { ...rendered, table };
}

async function fillWebhook(
  user: User,
  name: string,
  address: string,
): Promise<void> {
  const form = screen.getByRole("form", { name: "Register a channel" });
  await user.selectOptions(within(form).getByLabelText("Kind"), "webhook");
  await user.type(within(form).getByLabelText("Name"), name);
  await user.type(within(form).getByLabelText("Webhook address"), address);
  await user.type(
    within(form).getByLabelText("Shared secret"),
    "hook-secret-key",
  );
}

function cachedText(queryClient: QueryClient): string {
  return JSON.stringify([
    queryClient
      .getMutationCache()
      .getAll()
      .map((mutation) => mutation.state),
    queryClient
      .getQueryCache()
      .getAll()
      .map((query) => query.state),
  ]);
}

it("offers notifications from the administration index", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/admin");
  await user.click(await screen.findByRole("link", { name: "Notifications" }));
  expect(
    await screen.findByRole("heading", { name: "Notifications", level: 1 }),
  ).toBeVisible();
  expect(document.title).toBe("Notifications | Bloom");
});

it("hides notifications without admin.settings", async () => {
  setMockPermissions(["requests.approve"]);
  signInMockSession();
  renderApp("/admin");
  expect(await screen.findByRole("link", { name: "Requests" })).toBeVisible();
  expect(
    screen.queryByRole("link", { name: "Notifications" }),
  ).not.toBeInTheDocument();
});

it("lists channels with kind, state, and events", async () => {
  const { table } = await openChannels();
  const discord = within(table).getByRole("row", { name: /^chat-ops / });
  expect(discord).toHaveTextContent("Discord");
  expect(discord).toHaveTextContent("Enabled");
  expect(discord).toHaveTextContent("Degraded");
  expect(discord).toHaveTextContent("Request approved, Available to watch");
  const webhook = within(table).getByRole("row", { name: /^ops-hooks / });
  expect(webhook).toHaveTextContent("Webhook");
  expect(webhook).not.toHaveTextContent("Degraded");
});

it("registers a webhook and keeps the secret out of the cache", async () => {
  const user = userEvent.setup();
  const { queryClient, table } = await openChannels();
  await fillWebhook(user, "alerts", "https://hooks.example/alerts");

  await user.click(screen.getByRole("button", { name: "Register channel" }));

  expect(await screen.findByText("Saved alerts.")).toBeVisible();
  expect(
    await within(table).findByRole("row", { name: /^alerts / }),
  ).toBeVisible();
  expect(screen.getByLabelText("Name")).toHaveValue("");
  expect(cachedText(queryClient)).not.toContain("hook-secret-key");
  expect(document.body.textContent).not.toContain("hook-secret-key");
});

it("refuses plaintext HTTP without the override before sending anything", async () => {
  const user = userEvent.setup();
  let posts = 0;
  server.use(
    http.post(
      "*/api/v1/notification-channels",
      jsonApi(() => {
        posts += 1;
        return envelope(500, "internal", "boom");
      }),
    ),
  );
  await openChannels();
  await fillWebhook(user, "lan", "http://10.0.0.9/hook");

  await user.click(screen.getByRole("button", { name: "Register channel" }));

  const override = screen.getByLabelText(/Allow plaintext HTTP/);
  expect(override).toBeInvalid();
  expect(override).toHaveFocus();
  expect(override).toHaveAccessibleDescription(
    "Plaintext HTTP needs the explicit override.",
  );
  expect(posts).toBe(0);
});

it("explains a destination that cannot be reached and keeps the input", async () => {
  const user = userEvent.setup();
  await openChannels();
  await fillWebhook(user, "far", `https://${UNREACHABLE_CHANNEL_HOST}/hook`);

  await user.click(screen.getByRole("button", { name: "Register channel" }));

  const alert = await screen.findByRole("alert");
  expect(alert).toHaveTextContent(
    "The channel could not be reached or refused the message.",
  );
  expect(alert).toHaveFocus();
  expect(screen.getByLabelText("Name")).toHaveValue("far");
});

it("edits a channel without resending its stored secrets", async () => {
  const user = userEvent.setup();
  let body: unknown;
  const webhook = mockNotificationChannels.find(
    (channel) => channel.id === CHANNEL_WEBHOOK_ID,
  );
  server.use(
    http.put(
      "*/api/v1/notification-channels/:id",
      jsonApi(async ({ request }) => {
        body = await request.json();
        return HttpResponse.json({ ...webhook, name: "ops-hooks-2" });
      }),
    ),
  );
  const { table } = await openChannels();
  await user.click(
    within(table).getByRole("button", { name: "Edit ops-hooks" }),
  );
  const heading = screen.getByRole("heading", {
    name: "Edit ops-hooks",
    level: 2,
  });
  expect(heading).toHaveFocus();
  const form = screen.getByRole("form", { name: "Edit channel ops-hooks" });
  expect(within(form).getByLabelText("Kind")).toHaveValue("webhook");
  expect(
    within(form).getByLabelText("Webhook address"),
  ).toHaveAccessibleDescription(/Stored\. Leave blank to keep it\./);
  const name = within(form).getByLabelText("Name");
  await user.clear(name);
  await user.type(name, "ops-hooks-2");

  await user.click(within(form).getByRole("button", { name: "Save channel" }));

  expect(await screen.findByText("Saved ops-hooks-2.")).toBeVisible();
  expect(body).toEqual({
    kind: "webhook",
    name: "ops-hooks-2",
    enabled: true,
    subscriptions: ["created", "approved", "declined", "available", "failed"],
    subject_template: "",
    body_template: "{{.Title}} is {{.Status}}",
    webhook: { allow_insecure: false, allow_private: true },
  });
  expect(
    screen.getByRole("form", { name: "Register a channel" }),
  ).toBeVisible();
});

it("reports a test send and its failure", async () => {
  const user = userEvent.setup();
  const { table } = await openChannels();
  await user.click(
    within(table).getByRole("button", { name: "Test ops-hooks" }),
  );
  expect(
    await screen.findByText("Test message sent through ops-hooks."),
  ).toBeVisible();

  await user.click(
    within(table).getByRole("button", { name: "Test chat-ops" }),
  );
  const alert = await screen.findByRole("alert");
  expect(alert).toHaveTextContent(
    "The channel could not be reached or refused the message.",
  );
});

it("shows the deliveries of one channel", async () => {
  const user = userEvent.setup();
  const { table } = await openChannels();
  await user.click(
    within(table).getByRole("button", { name: "Deliveries of ops-hooks" }),
  );
  const deliveries = await screen.findByRole("table", {
    name: "Deliveries of ops-hooks, newest first",
  });
  const failed = within(deliveries).getByRole("row", {
    name: /^Request approved /,
  });
  expect(failed).toHaveTextContent("failed");
  expect(failed).toHaveTextContent("destination answered 500");
  expect(
    within(deliveries).getByRole("row", { name: /^Available to watch / }),
  ).toHaveTextContent("sent");

  await user.click(
    within(table).getByRole("button", { name: "Deliveries of ops-hooks" }),
  );
  expect(
    screen.queryByRole("table", {
      name: "Deliveries of ops-hooks, newest first",
    }),
  ).not.toBeInTheDocument();
});

it("removes a channel after confirmation", async () => {
  const user = userEvent.setup();
  const { table } = await openChannels();
  await user.click(
    within(table).getByRole("button", { name: "Remove chat-ops" }),
  );
  await user.click(
    within(table).getByRole("button", { name: "Confirm removing chat-ops" }),
  );
  expect(
    await within(table).findByRole("button", { name: "Remove ops-hooks" }),
  ).toBeVisible();
  expect(
    within(table).queryByRole("row", { name: /^chat-ops / }),
  ).not.toBeInTheDocument();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});
