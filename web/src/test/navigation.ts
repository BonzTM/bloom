import {
  screen,
  within,
  type BoundFunctions,
  type queries,
} from "@testing-library/react";
import type { UserEvent } from "@testing-library/user-event";

// The sidebar, which lists each area of the app once.
export async function findSidebar(): Promise<BoundFunctions<typeof queries>> {
  return within(
    await screen.findByRole("navigation", { name: "Main navigation" }),
  );
}

// Opens an area from the sidebar, then one of its pages from the tabs
// above the page when the area has more than one.
export async function openArea(
  user: UserEvent,
  area: string,
  page?: string,
): Promise<void> {
  const sidebar = await findSidebar();
  await user.click(await sidebar.findByRole("link", { name: area }));
  if (page === undefined) {
    return;
  }
  const tabs = within(
    await screen.findByRole("navigation", { name: `${area} pages` }),
  );
  await user.click(tabs.getByRole("link", { name: page }));
}
