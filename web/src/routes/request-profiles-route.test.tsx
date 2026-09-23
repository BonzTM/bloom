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

async function fillProfile(user: User, name: string): Promise<void> {
  const form = screen.getByRole("form", { name: "Create a profile" });
  await user.type(within(form).getByLabelText("Name"), name);
  await user.click(within(form).getByLabelText("Movie"));
  await user.selectOptions(
    within(form).getByLabelText("Download manager"),
    "Radarr",
  );
  await user.type(
    within(form).getByLabelText("Download manager instance"),
    "radarr-4k",
  );
  await user.type(within(form).getByLabelText("Quality profile"), "Ultra-HD");
  await user.type(within(form).getByLabelText("Root folder"), "/data/movies4k");
  await user.type(within(form).getByLabelText("Tags"), "bloom, 4k");
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
  expect(within(table).getAllByRole("rowheader")[0]).toHaveTextContent(
    "Movies HD",
  );
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
  expect(
    screen.getByRole("form", { name: "Replace the TMDB key" }),
  ).toBeVisible();
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

it("asks for the key before sending anything", async () => {
  const user = userEvent.setup();
  await openSettings();
  const form = await screen.findByRole("form", { name: "Store the TMDB key" });

  await user.click(within(form).getByRole("button", { name: "Store key" }));

  const field = within(form).getByLabelText("API key");
  expect(field).toBeInvalid();
  expect(field).toHaveFocus();
  expect(field).toHaveAccessibleDescription("Enter the TMDB API key.");
});

it("removes a stored key after an in-row confirmation", async () => {
  const user = userEvent.setup();
  setMockTmdbKeyConfigured(true);
  await openSettings();
  expect(await screen.findByText("Key stored")).toBeVisible();

  await user.click(screen.getByRole("button", { name: "Remove the TMDB key" }));
  expect(
    screen.getByRole("button", { name: "Confirm removing the TMDB key" }),
  ).toHaveFocus();
  await user.click(
    screen.getByRole("button", { name: "Cancel removing the TMDB key" }),
  );
  expect(
    screen.getByRole("button", { name: "Remove the TMDB key" }),
  ).toHaveFocus();

  await user.click(screen.getByRole("button", { name: "Remove the TMDB key" }));
  await user.click(
    screen.getByRole("button", { name: "Confirm removing the TMDB key" }),
  );

  expect(await screen.findByText("No key stored")).toBeVisible();
  expect(
    screen.queryByRole("button", { name: "Remove the TMDB key" }),
  ).not.toBeInTheDocument();
});

it("creates a profile and lists it", async () => {
  const user = userEvent.setup();
  const { table } = await openSettings();
  await fillProfile(user, "Movies 4K");

  await user.click(screen.getByRole("button", { name: "Create profile" }));

  expect(await screen.findByText("Saved Movies 4K.")).toBeVisible();
  const row = await within(table).findByRole("row", { name: /^Movies 4K / });
  expect(row).toHaveTextContent("radarr · radarr-4k");
  expect(row).toHaveTextContent("bloom, 4k");
  expect(within(table).getAllByRole("rowheader")[0]).toHaveTextContent(
    "Movies 4K",
  );
  expect(screen.getByLabelText("Name")).toHaveValue("");
});

it("explains what is missing before sending anything", async () => {
  const user = userEvent.setup();
  await openSettings();

  await user.click(screen.getByRole("button", { name: "Create profile" }));

  const name = screen.getByLabelText("Name");
  expect(name).toBeInvalid();
  expect(name).toHaveFocus();
  expect(name).toHaveAccessibleDescription("Enter the profile name.");
  expect(
    screen.getByRole("group", { name: "Media kinds" }),
  ).toHaveAccessibleDescription(
    "Choose at least one kind of media this profile accepts.",
  );
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
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

it("edits a profile in place and returns to creating", async () => {
  const user = userEvent.setup();
  const { table } = await openSettings();

  await user.click(
    within(table).getByRole("button", { name: "Edit Movies HD" }),
  );

  const heading = screen.getByRole("heading", {
    name: "Edit Movies HD",
    level: 2,
  });
  expect(heading).toHaveFocus();
  const form = screen.getByRole("form", { name: "Edit profile Movies HD" });
  expect(within(form).getByLabelText("Name")).toHaveValue("Movies HD");
  expect(within(form).getByLabelText("Movie")).toBeChecked();
  expect(within(form).getByLabelText("Series")).not.toBeChecked();
  expect(within(form).getByLabelText("Download manager")).toHaveValue("radarr");
  expect(within(form).getByLabelText("Tags")).toHaveValue("bloom");

  await user.clear(within(form).getByLabelText("Quality profile"));
  await user.type(within(form).getByLabelText("Quality profile"), "HD-720p");
  await user.click(within(form).getByRole("button", { name: "Save profile" }));

  expect(await screen.findByText("Saved Movies HD.")).toBeVisible();
  expect(
    await within(table).findByRole("row", { name: /^Movies HD .*HD-720p/ }),
  ).toBeVisible();
  expect(
    screen.getByRole("heading", { name: "Create a profile", level: 2 }),
  ).toBeVisible();
  expect(screen.getByLabelText("Name")).toHaveValue("");
});

it("cancels editing without saving", async () => {
  const user = userEvent.setup();
  const { table } = await openSettings();
  await user.click(within(table).getByRole("button", { name: "Edit Series" }));
  await user.click(screen.getByRole("button", { name: "Cancel editing" }));
  expect(
    screen.getByRole("heading", { name: "Create a profile", level: 2 }),
  ).toBeVisible();
  expect(screen.getByLabelText("Name")).toHaveValue("");
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
  expect(
    within(table).getByRole("button", { name: "Confirm removing Series" }),
  ).toHaveFocus();
  await user.click(
    within(table).getByRole("button", { name: "Cancel removing Series" }),
  );
  expect(
    within(table).getByRole("button", { name: "Remove Series" }),
  ).toHaveFocus();

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
  expect(
    within(table).getByRole("button", { name: "Remove Movies HD" }),
  ).toBeVisible();
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

it("sends one request when the profile form is submitted twice", async () => {
  const user = userEvent.setup();
  let requests = 0;
  server.use(
    http.post(
      "*/api/v1/request-profiles",
      jsonApi(() => {
        requests += 1;
        return undefined;
      }),
    ),
  );
  await openSettings();
  await fillProfile(user, "Twice");

  await user.dblClick(screen.getByRole("button", { name: "Create profile" }));

  expect(await screen.findByText("Saved Twice.")).toBeVisible();
  expect(requests).toBe(1);
});
