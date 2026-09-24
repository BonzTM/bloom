import { expect, it } from "@jest/globals";
import type { QueryClient } from "@tanstack/react-query";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import {
  envelope,
  INVALID_TMDB_KEY,
  jsonApi,
  setMockPermissions,
  setMockTmdbKeyConfigured,
  signInMockSession,
} from "../mocks/handlers.js";
import { renderApp, type AppRender } from "../test/render-app.js";
import { server } from "../test/server.js";

type User = ReturnType<typeof userEvent.setup>;

async function openSettings(): Promise<AppRender & { table: HTMLElement }> {
  signInMockSession();
  const rendered = renderApp("/admin/request-profiles");
  const table = await screen.findByRole("table", {
    name: "Request profiles, ordered by name",
  });
  return { ...rendered, table };
}

// Chooses the Radarr instance, waits for its options, and fills the rest.
async function fillProfile(user: User, name: string): Promise<void> {
  const form = await screen.findByRole("form", { name: "Create a profile" });
  await user.type(within(form).getByLabelText("Name"), name);
  await user.selectOptions(
    within(form).getByLabelText("Download manager"),
    "radarr-main (Radarr)",
  );
  await user.selectOptions(
    await within(form).findByLabelText("Quality profile"),
    "Ultra-HD",
  );
  await user.selectOptions(
    within(form).getByLabelText("Root folder"),
    "/data/movies4k",
  );
  await user.click(within(form).getByLabelText("bloom"));
  await user.click(within(form).getByLabelText("4k"));
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

it("offers request settings from the administration index", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/admin");
  await user.click(
    await screen.findByRole("link", { name: "Request settings" }),
  );
  expect(
    await screen.findByRole("heading", { name: "Request settings", level: 1 }),
  ).toBeVisible();
  expect(document.title).toBe("Request settings | Bloom");
});

it("hides request settings without admin.settings", async () => {
  setMockPermissions(["requests.approve"]);
  signInMockSession();
  renderApp("/admin");
  expect(await screen.findByRole("link", { name: "Requests" })).toBeVisible();
  expect(
    screen.queryByRole("link", { name: "Request settings" }),
  ).not.toBeInTheDocument();
});

it("lists profiles with their kinds, manager, folder, and tags", async () => {
  const { table } = await openSettings();
  const movies = within(table).getByRole("row", { name: /^Movies HD / });
  expect(movies).toHaveTextContent("Movie");
  expect(movies).toHaveTextContent("radarr · radarr-main");
  expect(movies).toHaveTextContent("HD-1080p");
  expect(movies).toHaveTextContent("/data/movies");
  expect(movies).toHaveTextContent("bloom");
  expect(
    within(movies).getByRole("button", { name: "Edit Movies HD" }),
  ).toBeVisible();
});

it("stores a TMDB key without keeping it anywhere on the client", async () => {
  const user = userEvent.setup();
  const { queryClient } = await openSettings();
  expect(await screen.findByText("No key stored")).toBeVisible();
  const form = screen.getByRole("form", { name: "Store the TMDB key" });
  await user.type(within(form).getByLabelText("API key"), "tmdb-secret-123");

  await user.click(within(form).getByRole("button", { name: "Store key" }));

  expect(await screen.findByText("Key stored")).toBeVisible();
  expect(screen.getByText("The TMDB key was saved.")).toBeVisible();
  expect(screen.getByLabelText("New API key")).toHaveValue("");
  expect(cachedText(queryClient)).not.toContain("tmdb-secret-123");
  expect(document.body.textContent).not.toContain("tmdb-secret-123");
});

it("explains a key the server refuses and keeps it in the field", async () => {
  const user = userEvent.setup();
  await openSettings();
  const form = await screen.findByRole("form", { name: "Store the TMDB key" });
  await user.type(within(form).getByLabelText("API key"), INVALID_TMDB_KEY);

  await user.click(within(form).getByRole("button", { name: "Store key" }));

  const alert = await within(form).findByRole("alert");
  expect(alert).toHaveTextContent("Enter a TMDB API key without control");
  expect(alert).toHaveFocus();
  expect(within(form).getByLabelText("API key")).toHaveValue(INVALID_TMDB_KEY);
});

it("removes a stored key after an in-row confirmation", async () => {
  const user = userEvent.setup();
  setMockTmdbKeyConfigured(true);
  await openSettings();
  expect(await screen.findByText("Key stored")).toBeVisible();
  await user.click(screen.getByRole("button", { name: "Remove the TMDB key" }));
  await user.click(
    screen.getByRole("button", { name: "Confirm removing the TMDB key" }),
  );
  expect(await screen.findByText("No key stored")).toBeVisible();
});

it("creates a profile from a registered instance's options", async () => {
  const user = userEvent.setup();
  const { table } = await openSettings();
  await fillProfile(user, "Movies 4K");

  await user.click(screen.getByRole("button", { name: "Create profile" }));

  expect(await screen.findByText("Saved Movies 4K.")).toBeVisible();
  const row = await within(table).findByRole("row", { name: /^Movies 4K / });
  expect(row).toHaveTextContent("radarr · radarr-main");
  expect(row).toHaveTextContent("Ultra-HD");
  expect(row).toHaveTextContent("/data/movies4k");
  expect(row).toHaveTextContent("bloom, 4k");
  expect(screen.getByLabelText("Name")).toHaveValue("");
});

it("asks for an instance before showing its options", async () => {
  const user = userEvent.setup();
  await openSettings();
  const form = await screen.findByRole("form", { name: "Create a profile" });
  expect(
    within(form).getByText(
      "Choose an instance to pick its quality profile and root folder.",
    ),
  ).toBeVisible();
  expect(
    within(form).queryByLabelText("Quality profile"),
  ).not.toBeInTheDocument();

  await user.click(
    within(form).getByRole("button", { name: "Create profile" }),
  );

  const name = within(form).getByLabelText("Name");
  expect(name).toBeInvalid();
  expect(name).toHaveFocus();
  expect(within(form).getByLabelText("Download manager")).toBeInvalid();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

it("preselects the only choice an instance offers", async () => {
  const user = userEvent.setup();
  await openSettings();
  const form = await screen.findByRole("form", { name: "Create a profile" });
  await user.selectOptions(
    within(form).getByLabelText("Download manager"),
    "sonarr-main (Sonarr)",
  );
  expect(await within(form).findByLabelText("Quality profile")).toHaveValue(
    "Any",
  );
  expect(within(form).getByLabelText("Root folder")).toHaveValue("/data/tv");
  expect(within(form).getByText("This instance has no tags.")).toBeVisible();
});

it("points at registration when no instance exists", async () => {
  server.use(
    http.get(
      "*/api/v1/download-managers",
      jsonApi(() => HttpResponse.json({ items: [], next_cursor: "" })),
    ),
  );
  await openSettings();
  expect(
    await screen.findByRole("link", { name: "Register one first." }),
  ).toHaveAttribute("href", "/admin/download-managers");
});

it("reports a name another profile already uses", async () => {
  const user = userEvent.setup();
  await openSettings();
  await fillProfile(user, "series");

  await user.click(screen.getByRole("button", { name: "Create profile" }));

  const alert = await screen.findByRole("alert");
  expect(alert).toHaveTextContent("Another profile already uses that name");
  expect(alert).toHaveFocus();
  expect(screen.getByLabelText("Name")).toHaveValue("series");
});

it("edits a profile with its instance and options preselected", async () => {
  const user = userEvent.setup();
  const { table } = await openSettings();

  await user.click(
    within(table).getByRole("button", { name: "Edit Movies HD" }),
  );

  expect(
    screen.getByRole("heading", { name: "Edit Movies HD", level: 2 }),
  ).toHaveFocus();
  const form = screen.getByRole("form", { name: "Edit profile Movies HD" });
  expect(within(form).getByLabelText("Name")).toHaveValue("Movies HD");
  expect(within(form).getByLabelText("Download manager")).toHaveValue(
    "7d8e9f0a-0000-4000-8000-000000000001",
  );
  expect(await within(form).findByLabelText("Quality profile")).toHaveValue(
    "HD-1080p",
  );
  expect(within(form).getByLabelText("bloom")).toBeChecked();
  expect(within(form).getByLabelText("4k")).not.toBeChecked();

  await user.selectOptions(
    within(form).getByLabelText("Quality profile"),
    "Ultra-HD",
  );
  await user.click(within(form).getByRole("button", { name: "Save profile" }));

  expect(await screen.findByText("Saved Movies HD.")).toBeVisible();
  expect(
    await within(table).findByRole("row", { name: /^Movies HD .*Ultra-HD/ }),
  ).toBeVisible();
  expect(
    screen.getByRole("heading", { name: "Create a profile", level: 2 }),
  ).toBeVisible();
});

it("removes a profile after an in-row confirmation", async () => {
  const user = userEvent.setup();
  server.use(
    http.delete(
      "*/api/v1/request-profiles/:id",
      jsonApi(() => new HttpResponse(null, { status: 204 })),
    ),
  );
  const { table } = await openSettings();
  await user.click(
    within(table).getByRole("button", { name: "Remove Series" }),
  );
  await user.click(
    within(table).getByRole("button", { name: "Confirm removing Series" }),
  );
  expect(
    await within(table).findByRole("button", { name: "Remove Movies HD" }),
  ).toBeVisible();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

it("explains why a profile still in use cannot be removed", async () => {
  const user = userEvent.setup();
  const { table } = await openSettings();
  await user.click(
    within(table).getByRole("button", { name: "Remove Movies HD" }),
  );
  await user.click(
    within(table).getByRole("button", { name: "Confirm removing Movies HD" }),
  );
  const alert = await screen.findByRole("alert");
  expect(alert).toHaveTextContent("requests still reference this one");
  expect(alert).toHaveFocus();
});

it("offers a retry when the profiles cannot be listed", async () => {
  const user = userEvent.setup();
  let failing = true;
  server.use(
    http.get(
      "*/api/v1/request-profiles",
      jsonApi(() => (failing ? envelope(500, "internal", "boom") : undefined)),
    ),
  );
  signInMockSession();
  renderApp("/admin/request-profiles");
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "The profiles could not be loaded.",
  );
  failing = false;
  await user.click(screen.getByRole("button", { name: "Retry" }));
  expect(
    await screen.findByRole("table", {
      name: "Request profiles, ordered by name",
    }),
  ).toBeVisible();
});
