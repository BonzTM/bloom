import { expect, it } from "@jest/globals";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  BUSY_MEDIA_SERVER_HOST,
  envelope,
  jsonApi,
  mockSession,
  setMockPermissions,
  signInMockSession,
  UNREACHABLE_MEDIA_SERVER_HOST,
} from "../mocks/handlers.js";
import { http, HttpResponse } from "msw";
import { renderApp, type AppRender } from "../test/render-app.js";
import { server } from "../test/server.js";
import type { QueryClient } from "@tanstack/react-query";

type User = ReturnType<typeof userEvent.setup>;

async function openMediaServers(): Promise<AppRender & { table: HTMLElement }> {
  signInMockSession();
  const rendered = renderApp("/admin/media-servers");
  const table = await screen.findByRole("table", {
    name: "Media servers, ordered by name",
  });
  return { ...rendered, table };
}

// Everything the mutation cache would hand to an inspector: variables,
// context, data, and errors of every mutation, serialised.
function cachedMutationText(queryClient: QueryClient): string {
  return JSON.stringify(
    queryClient
      .getMutationCache()
      .getAll()
      .map((mutation) => mutation.state),
  );
}

async function fillRegistration(
  user: User,
  fields: Readonly<{ name: string; address: string; apiKey?: string }>,
): Promise<void> {
  await user.type(screen.getByLabelText("Name"), fields.name);
  await user.type(screen.getByLabelText("Address"), fields.address);
  await user.type(screen.getByLabelText("API key"), fields.apiKey ?? "k3y");
}

it("offers the media servers page only to an account with admin.settings", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp();
  await user.click(await screen.findByRole("link", { name: "Admin" }));
  await user.click(await screen.findByRole("link", { name: "Media servers" }));
  expect(
    await screen.findByRole("heading", { name: "Media servers", level: 1 }),
  ).toBeVisible();
  expect(document.title).toBe("Media servers | Bloom");
});

it("hides the media servers link without admin.settings", async () => {
  setMockPermissions(["admin.roles"]);
  signInMockSession();
  renderApp("/admin");
  expect(await screen.findByRole("link", { name: "Roles" })).toBeVisible();
  expect(
    screen.queryByRole("link", { name: "Media servers" }),
  ).not.toBeInTheDocument();
});

it("lists the registered servers with transport, capabilities, and date", async () => {
  const { table } = await openMediaServers();
  const cabin = within(table).getByRole("row", { name: /^Cabin / });
  expect(cabin).toHaveTextContent("Jellyfin");
  expect(cabin).toHaveTextContent("http://10.0.0.5:8096");
  expect(cabin).toHaveTextContent("Plaintext HTTP allowed");
  expect(cabin).toHaveTextContent("2026-09-12");
  const cabinCapabilities = within(cabin).getByRole("list", {
    name: "Capabilities of Cabin",
  });
  expect(cabinCapabilities).toHaveTextContent("Create users");
  expect(cabinCapabilities).not.toHaveTextContent("Quick Connect");

  const livingRoom = within(table).getByRole("row", { name: /^Living room / });
  expect(livingRoom).not.toHaveTextContent("Plaintext HTTP allowed");
  expect(
    within(livingRoom).getAllByRole("listitem", { name: "" }),
  ).toHaveLength(4);
  expect(within(table).getAllByRole("rowheader")).toHaveLength(2);
});

it("registers a server, reports what answered, and clears the API key", async () => {
  const user = userEvent.setup();
  const { queryClient } = await openMediaServers();
  await fillRegistration(user, {
    name: "Office",
    address: "https://office.example",
  });

  await user.click(screen.getByRole("button", { name: "Register server" }));

  expect(await screen.findByText(/Registered Office/)).toHaveTextContent(
    "Registered Office: Mock Jellyfin version 10.10.7.",
  );
  expect(
    await screen.findByRole("rowheader", { name: "Office" }),
  ).toBeVisible();
  expect(screen.getByLabelText("API key")).toHaveValue("");
  expect(screen.getByLabelText("Name")).toHaveValue("");
  expect(cachedMutationText(queryClient)).not.toContain("k3y");
});

it("explains plaintext HTTP before sending anything", async () => {
  const user = userEvent.setup();
  await openMediaServers();
  await fillRegistration(user, {
    name: "Shed",
    address: "http://10.0.0.9:8096",
  });

  await user.click(screen.getByRole("button", { name: "Register server" }));

  const address = screen.getByLabelText("Address");
  expect(address).toHaveAccessibleDescription(/allow plaintext HTTP below/);
  expect(address).toBeInvalid();
  expect(address).toHaveFocus();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();

  await user.click(
    screen.getByRole("checkbox", { name: /Allow plaintext HTTP/ }),
  );
  await user.click(screen.getByRole("button", { name: "Register server" }));
  expect(await screen.findByRole("rowheader", { name: "Shed" })).toBeVisible();
  expect(
    within(screen.getByRole("row", { name: /^Shed / })).getByText(
      "Plaintext HTTP allowed",
    ),
  ).toBeVisible();
});

it("reports a name that is already in use", async () => {
  const user = userEvent.setup();
  const { queryClient } = await openMediaServers();
  await fillRegistration(user, {
    name: "cabin",
    address: "https://cabin.example",
  });

  await user.click(screen.getByRole("button", { name: "Register server" }));

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Another media server already uses that name.",
  );
  expect(screen.getByRole("alert")).toHaveFocus();
  expect(screen.getByLabelText("API key")).toHaveValue("k3y");
  expect(cachedMutationText(queryClient)).not.toContain("k3y");
});

it("reports a server that did not answer as Jellyfin", async () => {
  const user = userEvent.setup();
  await openMediaServers();
  await fillRegistration(user, {
    name: "Attic",
    address: `https://${UNREACHABLE_MEDIA_SERVER_HOST}`,
  });

  await user.click(screen.getByRole("button", { name: "Register server" }));

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "did not answer as a Jellyfin server, or it rejected the API key. Nothing was saved.",
  );
  expect(
    screen.queryByRole("rowheader", { name: "Attic" }),
  ).not.toBeInTheDocument();
});

it("reports a busy server with the wait it asked for", async () => {
  const user = userEvent.setup();
  await openMediaServers();
  await fillRegistration(user, {
    name: "Garage",
    address: `https://${BUSY_MEDIA_SERVER_HOST}`,
  });

  await user.click(screen.getByRole("button", { name: "Register server" }));

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "That server is busy right now. Try again in 2 seconds.",
  );
});

it("removes a server after an in-row confirmation", async () => {
  const user = userEvent.setup();
  const { table } = await openMediaServers();

  await user.click(within(table).getByRole("button", { name: "Remove Cabin" }));
  expect(
    within(table).getByRole("button", { name: "Confirm removal of Cabin" }),
  ).toHaveFocus();
  await user.click(
    within(table).getByRole("button", { name: "Cancel removal of Cabin" }),
  );
  expect(within(table).getByRole("rowheader", { name: "Cabin" })).toBeVisible();
  expect(
    within(table).queryByRole("button", { name: "Confirm removal of Cabin" }),
  ).not.toBeInTheDocument();
  expect(
    within(table).getByRole("button", { name: "Remove Cabin" }),
  ).toHaveFocus();

  await user.click(within(table).getByRole("button", { name: "Remove Cabin" }));
  await user.click(
    within(table).getByRole("button", { name: "Confirm removal of Cabin" }),
  );

  await within(table).findByRole("rowheader", { name: "Living room" });
  expect(
    within(table).queryByRole("rowheader", { name: "Cabin" }),
  ).not.toBeInTheDocument();
  expect(within(table).getAllByRole("rowheader")).toHaveLength(1);
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

it("re-reads the session and denies the page when a registration is forbidden", async () => {
  const user = userEvent.setup();
  await openMediaServers();
  let sessionReads = 0;
  server.use(
    http.get(
      "*/api/v1/auth/me",
      jsonApi(() => {
        sessionReads += 1;
        return HttpResponse.json(mockSession);
      }),
    ),
    http.post(
      "*/api/v1/media-servers",
      jsonApi(() => envelope(403, "forbidden", "missing permission")),
    ),
  );
  await fillRegistration(user, {
    name: "Office",
    address: "https://office.example",
  });

  await user.click(screen.getByRole("button", { name: "Register server" }));

  expect(
    await screen.findByRole("heading", { name: "Access denied", level: 1 }),
  ).toBeVisible();
  expect(sessionReads).toBeGreaterThanOrEqual(1);
});

it("reports a cross-site rejection as a plain failure, not a lost right", async () => {
  const user = userEvent.setup();
  await openMediaServers();
  server.use(
    http.post(
      "*/api/v1/media-servers",
      jsonApi(() => envelope(403, "csrf_rejected", "cross-site request")),
    ),
  );
  await fillRegistration(user, {
    name: "Office",
    address: "https://office.example",
  });

  await user.click(screen.getByRole("button", { name: "Register server" }));

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "The request was refused as cross-site. Reload the page and try again.",
  );
  expect(
    screen.queryByRole("heading", { name: "Access denied" }),
  ).not.toBeInTheDocument();
  expect(screen.getByLabelText("API key")).toHaveValue("k3y");
});
