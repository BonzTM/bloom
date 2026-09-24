import { expect, it } from "@jest/globals";
import type { QueryClient } from "@tanstack/react-query";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import {
  envelope,
  jsonApi,
  setMockPermissions,
  signInMockSession,
  UNREACHABLE_MANAGER_HOST,
} from "../mocks/handlers.js";
import { renderApp, type AppRender } from "../test/render-app.js";
import { server } from "../test/server.js";

type User = ReturnType<typeof userEvent.setup>;

async function openManagers(): Promise<AppRender & { table: HTMLElement }> {
  signInMockSession();
  const rendered = renderApp("/admin/download-managers");
  const table = await screen.findByRole("table", {
    name: "Download managers, ordered by name",
  });
  return { ...rendered, table };
}

async function fillManager(
  user: User,
  name: string,
  address: string,
): Promise<void> {
  const form = screen.getByRole("form", {
    name: "Register a download manager",
  });
  await user.selectOptions(within(form).getByLabelText("Kind"), "radarr");
  await user.type(within(form).getByLabelText("Name"), name);
  await user.type(within(form).getByLabelText("Address"), address);
  await user.type(within(form).getByLabelText("API key"), "manager-secret-key");
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

it("offers download managers from the administration index", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/admin");
  await user.click(
    await screen.findByRole("link", { name: "Download managers" }),
  );
  expect(
    await screen.findByRole("heading", { name: "Download managers", level: 1 }),
  ).toBeVisible();
  expect(document.title).toBe("Download managers | Bloom");
});

it("hides download managers without admin.settings", async () => {
  setMockPermissions(["requests.approve"]);
  signInMockSession();
  renderApp("/admin");
  expect(await screen.findByRole("link", { name: "Requests" })).toBeVisible();
  expect(
    screen.queryByRole("link", { name: "Download managers" }),
  ).not.toBeInTheDocument();
});

it("lists instances with kind, address, and transport", async () => {
  const { table } = await openManagers();
  const radarr = within(table).getByRole("row", { name: /^radarr-main / });
  expect(radarr).toHaveTextContent("Radarr");
  expect(radarr).toHaveTextContent("https://radarr.example");
  expect(radarr).toHaveTextContent("HTTPS");
  const sonarr = within(table).getByRole("row", { name: /^sonarr-main / });
  expect(sonarr).toHaveTextContent("Sonarr");
  expect(sonarr).toHaveTextContent("Plaintext HTTP");
});

it("registers an instance and keeps the key out of the cache", async () => {
  const user = userEvent.setup();
  const { queryClient, table } = await openManagers();
  await fillManager(user, "radarr-4k", "https://radarr4k.example");

  await user.click(
    screen.getByRole("button", { name: "Register download manager" }),
  );

  expect(
    await screen.findByText("Registered radarr-4k: Radarr version 5.0.0."),
  ).toBeVisible();
  expect(
    await within(table).findByRole("row", { name: /^radarr-4k / }),
  ).toBeVisible();
  expect(screen.getByLabelText("API key")).toHaveValue("");
  expect(cachedText(queryClient)).not.toContain("manager-secret-key");
  expect(document.body.textContent).not.toContain("manager-secret-key");
});

it("refuses plaintext HTTP without the override before sending anything", async () => {
  const user = userEvent.setup();
  let posts = 0;
  server.use(
    http.post(
      "*/api/v1/download-managers",
      jsonApi(() => {
        posts += 1;
        return envelope(500, "internal", "boom");
      }),
    ),
  );
  await openManagers();
  await fillManager(user, "lan", "http://10.0.0.9:7878");

  await user.click(
    screen.getByRole("button", { name: "Register download manager" }),
  );

  const override = screen.getByLabelText(/Allow plaintext HTTP/);
  expect(override).toBeInvalid();
  expect(override).toHaveFocus();
  expect(override).toHaveAccessibleDescription(
    "Plaintext HTTP needs the explicit override.",
  );
  expect(posts).toBe(0);
});

it("explains an instance that cannot be reached", async () => {
  const user = userEvent.setup();
  await openManagers();
  await fillManager(user, "far", `https://${UNREACHABLE_MANAGER_HOST}`);

  await user.click(
    screen.getByRole("button", { name: "Register download manager" }),
  );

  const alert = await screen.findByRole("alert");
  expect(alert).toHaveTextContent(
    "The instance could not be reached or refused the API key.",
  );
  expect(alert).toHaveFocus();
  expect(screen.getByLabelText("Name")).toHaveValue("far");
});

it("refuses to remove an instance a profile still uses", async () => {
  const user = userEvent.setup();
  const { table } = await openManagers();
  await user.click(
    within(table).getByRole("button", { name: "Remove radarr-main" }),
  );
  await user.click(
    within(table).getByRole("button", { name: "Confirm removing radarr-main" }),
  );
  const alert = await screen.findByRole("alert");
  expect(alert).toHaveTextContent(
    "a request profile still references this one",
  );
  expect(alert).toHaveFocus();
});

it("removes an unused instance after confirmation", async () => {
  const user = userEvent.setup();
  server.use(
    http.get(
      "*/api/v1/request-profiles",
      jsonApi(() => HttpResponse.json({ items: [], next_cursor: "" })),
    ),
    http.delete(
      "*/api/v1/download-managers/:id",
      jsonApi(() => new HttpResponse(null, { status: 204 })),
    ),
  );
  const { table } = await openManagers();
  await user.click(
    within(table).getByRole("button", { name: "Remove sonarr-main" }),
  );
  await user.click(
    within(table).getByRole("button", { name: "Confirm removing sonarr-main" }),
  );
  expect(
    await within(table).findByRole("button", { name: "Remove radarr-main" }),
  ).toBeVisible();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});
