import { expect, it } from "@jest/globals";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import {
  envelope,
  jsonApi,
  mockPlaybackHistory,
  setMockPermissions,
  signInMockSession,
} from "../mocks/handlers.js";
import { renderApp } from "../test/render-app.js";
import { server } from "../test/server.js";

async function openPlayback(): Promise<HTMLElement> {
  signInMockSession();
  renderApp("/admin/playback");
  return screen.findByRole("table", { name: "Playing now, newest first" });
}

it("offers the playback page from the administration index", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/admin");
  await user.click(await screen.findByRole("link", { name: "Playback" }));
  expect(
    await screen.findByRole("heading", { name: "Playback", level: 1 }),
  ).toBeVisible();
  expect(document.title).toBe("Playback | Bloom");
});

it("hides the playback link without stats.read.all", async () => {
  setMockPermissions(["admin.roles"]);
  signInMockSession();
  renderApp("/admin");
  expect(await screen.findByRole("link", { name: "Roles" })).toBeVisible();
  expect(
    screen.queryByRole("link", { name: "Playback" }),
  ).not.toBeInTheDocument();
});

it("shows who is watching what, where, and how", async () => {
  const table = await openPlayback();
  const alice = within(table).getByRole("row", { name: /^alice / });
  expect(alice).toHaveTextContent("Fringe S01E01 · Pilot");
  expect(alice).toHaveTextContent("Episode");
  expect(alice).toHaveTextContent("Living room TV (Jellyfin Web)");
  expect(alice).toHaveTextContent("Cabin");
  expect(alice).toHaveTextContent("12:34");
  expect(alice).toHaveTextContent("Direct play");
  expect(alice).toHaveTextContent("12 min 34 s");
  const bob = within(table).getByRole("row", { name: /^bob / });
  expect(bob).toHaveTextContent("Heat");
  expect(bob).toHaveTextContent("1:30:00 paused");
  expect(bob).toHaveTextContent("Transcode");
  expect(bob).toHaveTextContent("1 h 0 min");
});

it("lists finished watches and filters them by server", async () => {
  const user = userEvent.setup();
  await openPlayback();
  const history = await screen.findByRole("table", {
    name: "Finished watches, newest first",
  });
  expect(within(history).getAllByRole("rowheader")).toHaveLength(2);
  expect(within(history).getByRole("row", { name: /^bob / })).toHaveTextContent(
    "2 h 0 min",
  );

  await user.selectOptions(screen.getByLabelText("Server"), "Living room");

  const filtered = await screen.findByRole("table", {
    name: "Finished watches, newest first",
  });
  expect(
    await within(filtered).findByRole("row", { name: /^bob / }),
  ).toHaveTextContent("Ronin");
  expect(
    within(filtered).queryByRole("row", { name: /^alice / }),
  ).not.toBeInTheDocument();
});

it("says so when nothing is playing", async () => {
  server.use(
    http.get(
      "*/api/v1/playback/now",
      jsonApi(() => HttpResponse.json({ items: [] })),
    ),
  );
  signInMockSession();
  renderApp("/admin/playback");
  expect(
    await screen.findByText("Nothing is playing right now."),
  ).toBeVisible();
});

it("keeps every server in the filter after one is chosen, even with no rows", async () => {
  const user = userEvent.setup();
  // Both servers appear only in history: nothing is playing now.
  server.use(
    http.get(
      "*/api/v1/playback/now",
      jsonApi(() => HttpResponse.json({ items: [] })),
    ),
    http.get(
      "*/api/v1/playback/history",
      jsonApi(({ request }) => {
        const url = new URL(request.url);
        const serverId = url.searchParams.get("media_server_id");
        return serverId === null
          ? undefined
          : HttpResponse.json({ items: [], next_cursor: "" });
      }),
    ),
  );
  signInMockSession();
  renderApp("/admin/playback");
  const filter = await screen.findByLabelText("Server");
  expect(within(filter).getAllByRole("option")).toHaveLength(3);

  await user.selectOptions(filter, "Cabin");

  expect(
    await screen.findByText("No finished watches have been recorded yet."),
  ).toBeVisible();
  expect(screen.getByLabelText("Server")).toHaveValue(
    "3d7f1a2b-0000-4000-8000-000000000001",
  );
  expect(
    within(screen.getByLabelText("Server")).getAllByRole("option"),
  ).toHaveLength(3);
});

it("keeps the last rows and says so when a refresh fails, then recovers", async () => {
  const user = userEvent.setup();
  let failing = false;
  server.use(
    http.get(
      "*/api/v1/playback/now",
      jsonApi(() => (failing ? envelope(500, "internal", "boom") : undefined)),
    ),
  );
  signInMockSession();
  const { queryClient } = renderApp("/admin/playback");
  const nowTable = await screen.findByRole("table", {
    name: "Playing now, newest first",
  });

  failing = true;
  // The same path a timed refresh takes.
  await queryClient.refetchQueries({ queryKey: ["playback", "now"] });

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "What is playing could not be refreshed. What is shown may be out of date.",
  );
  expect(within(nowTable).getByRole("row", { name: /^alice / })).toBeVisible();

  failing = false;
  await user.click(screen.getByRole("button", { name: "Retry" }));
  await waitFor(() => {
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
  expect(within(nowTable).getByRole("row", { name: /^alice / })).toBeVisible();
});

it("offers a retry when what is playing cannot be loaded at all", async () => {
  const user = userEvent.setup();
  let failing = true;
  server.use(
    http.get(
      "*/api/v1/playback/now",
      jsonApi(() => (failing ? envelope(500, "internal", "boom") : undefined)),
    ),
  );
  signInMockSession();
  renderApp("/admin/playback");
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "What is playing could not be loaded.",
  );
  failing = false;
  await user.click(screen.getByRole("button", { name: "Retry" }));
  expect(
    await screen.findByRole("table", { name: "Playing now, newest first" }),
  ).toBeVisible();
});

it("loads the next page of history and reports a page that fails", async () => {
  const user = userEvent.setup();
  let secondPageFails = true;
  server.use(
    http.get(
      "*/api/v1/playback/history",
      jsonApi(({ request }) => {
        const cursor = new URL(request.url).searchParams.get("cursor");
        if (cursor === null) {
          return HttpResponse.json({
            items: [mockPlaybackHistory[0]],
            next_cursor: "offset:1",
          });
        }
        return secondPageFails
          ? envelope(500, "internal", "boom")
          : HttpResponse.json({
              items: [mockPlaybackHistory[1]],
              next_cursor: "",
            });
      }),
    ),
  );
  await openPlayback();
  await user.click(
    await screen.findByRole("button", { name: "Load more history" }),
  );
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "More history could not be loaded.",
  );
  secondPageFails = false;
  await user.click(screen.getByRole("button", { name: "Load more history" }));
  const history = await screen.findByRole("table", {
    name: "Finished watches, newest first",
  });
  await within(history).findByRole("row", { name: /^bob / });
  expect(within(history).getAllByRole("rowheader")).toHaveLength(2);
  expect(
    screen.queryByRole("button", { name: "Load more history" }),
  ).not.toBeInTheDocument();
});
