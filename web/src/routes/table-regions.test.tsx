import { expect, it } from "@jest/globals";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { signInMockSession } from "../mocks/handlers.js";
import { renderApp } from "../test/render-app.js";

// Wide tables scroll inside a named region that the keyboard can reach, so
// columns hidden by the scroll are never out of reach for keyboard users.
it.each([
  ["/admin/roles", "Roles table", "Roles, ordered by name"],
  [
    "/admin/media-servers",
    "Media servers table",
    "Media servers, ordered by name",
  ],
  ["/admin/invites", "Invites table", "Invites, newest first"],
  ["/admin/playback", "Playing now table", "Playing now, newest first"],
  [
    "/admin/playback",
    "Finished watches table",
    "Finished watches, newest first",
  ],
])(
  "%s wraps its table in a focusable named region",
  async (path, region, caption) => {
    signInMockSession();
    renderApp(path);
    const wrapper = await screen.findByRole("region", { name: region });
    expect(wrapper).toHaveAttribute("tabindex", "0");
    expect(within(wrapper).getByRole("table", { name: caption })).toBeVisible();
  },
);

it("reaches the region and then the row controls in order by keyboard", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/admin/invites");
  const wrapper = await screen.findByRole("region", { name: "Invites table" });
  wrapper.focus();
  expect(wrapper).toHaveFocus();

  await user.tab();

  expect(
    within(wrapper).getByRole("button", { name: "Revoke Family" }),
  ).toHaveFocus();
});
