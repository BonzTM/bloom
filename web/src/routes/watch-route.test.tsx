import { expect, it } from "@jest/globals";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { signInMockSession, WATCH_WITH_SERIES } from "../mocks/handlers.js";
import { renderApp } from "../test/render-app.js";

it("shows the stream on the playback tables and links to the series", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/admin/playback");
  const table = await screen.findByRole("table", {
    name: "Playing now, newest first",
  });
  const alice = within(table).getByRole("row", { name: /^alice / });
  expect(alice).toHaveTextContent("1080p · h264/aac · 8.2 Mbit/s");

  await user.click(
    within(alice).getByRole("link", { name: /^Details of Fringe S01E01/ }),
  );

  expect(
    await screen.findByRole("heading", { name: "Watch", level: 1 }),
  ).toBeVisible();
  expect(document.title).toBe("Watch | Bloom");
  const samples = await screen.findByRole("table", {
    name: "Samples, newest first",
  });
  const rows = within(samples).getAllByRole("row").slice(1);
  expect(rows).toHaveLength(2);
  expect(rows[0]).toHaveTextContent("Transcode");
  expect(rows[0]).toHaveTextContent("720p · h264/aac · 4.0 Mbit/s");
  expect(rows[0]).toHaveTextContent(
    "ContainerNotSupported, AudioCodecNotSupported",
  );
  expect(rows[1]).toHaveTextContent("Direct play");
  expect(rows[1]).toHaveTextContent("05:00");
  expect(
    screen.getByRole("link", { name: "Back to playback" }),
  ).toHaveAttribute("href", "/admin/playback");
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
