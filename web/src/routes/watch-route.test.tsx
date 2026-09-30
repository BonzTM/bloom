import { expect, it } from "@jest/globals";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { signInMockSession, WATCH_WITH_SERIES } from "../mocks/handlers.js";
import { renderApp } from "../test/render-app.js";

it("opens a watch from its card and shows it with artwork and facts", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/admin/playback");
  const cards = await screen.findByRole("list", { name: "Playing now" });
  const alice = within(cards).getByRole("article", { name: "alice" });
  // The card keeps to who, what, how far, and where; the stream lives on
  // the watch's own page.
  expect(alice).not.toHaveTextContent("h264");
  expect(alice).toHaveTextContent("Direct play");

  await user.click(
    within(alice).getByRole("link", { name: /^Details of Fringe S01E01/ }),
  );

  expect(
    await screen.findByRole("heading", { name: /^Fringe S01E01/, level: 1 }),
  ).toBeVisible();
  expect(document.title).toMatch(/^Fringe S01E01.*\| Bloom$/);
  // The stream in play comes from the newest sample once it has loaded.
  await screen.findByText("720p · h264/aac · 4.0 Mbit/s");
  expect(screen.getByText("Watched").nextElementSibling).toHaveTextContent(
    "12 min 34 s",
  );
  const timeline = await screen.findByRole("list", { name: "Watch timeline" });
  const events = within(timeline).getAllByRole("listitem");
  expect(events[0]).toHaveTextContent("Started at 5:00");
  expect(events[0]).toHaveTextContent("Direct play");
  expect(events[1]).toHaveTextContent("Switched to transcode at 12:34");
  expect(events[1]).toHaveTextContent("720p · h264/aac · 4.0 Mbit/s");
  expect(events[1]).toHaveTextContent(
    "because ContainerNotSupported, AudioCodecNotSupported",
  );
  expect(events.at(-1)).toHaveTextContent("Last seen at 12:34");
  expect(screen.queryByText(/All samples/)).not.toBeInTheDocument();
  expect(
    screen.getByRole("link", { name: "Back to playback" }),
  ).toHaveAttribute("href", "/admin/playback");
});

it("reads the watch itself on a cold deep link", async () => {
  signInMockSession();
  renderApp(`/admin/playback/watches/${WATCH_WITH_SERIES}`);
  expect(
    await screen.findByRole("heading", { name: /^Fringe S01E01/, level: 1 }),
  ).toBeVisible();
  expect(screen.getByText("Server").nextElementSibling).toHaveTextContent(
    "Cabin",
  );
  expect(
    await screen.findByRole("list", { name: "Watch timeline" }),
  ).toBeVisible();
});

it("shows not found for an unknown watch or a bad id", async () => {
  signInMockSession();
  renderApp("/admin/playback/watches/7b2c3d4e-0000-4000-8000-0000000000ff");
  expect(
    await screen.findByRole("heading", { name: /not found/i }),
  ).toBeVisible();
});

it("rejects an id that is not a uuid", async () => {
  signInMockSession();
  renderApp("/admin/playback/watches/not-a-uuid");
  expect(
    await screen.findByRole("heading", { name: /not found/i }),
  ).toBeVisible();
  expect(WATCH_WITH_SERIES).toMatch(/^[0-9a-f-]{36}$/);
});
