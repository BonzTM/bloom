import { afterEach, expect, it, jest } from "@jest/globals";
import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { MediaRow } from "./media-row.js";

const originalMatchMedia = Object.getOwnPropertyDescriptor(
  window,
  "matchMedia",
);

afterEach(() => {
  if (originalMatchMedia === undefined) {
    Reflect.deleteProperty(window, "matchMedia");
  } else {
    Object.defineProperty(window, "matchMedia", originalMatchMedia);
  }
});

function renderRow(cards = 12) {
  return render(
    <MemoryRouter>
      <MediaRow title="Trending" seeAll="/browse/trending">
        {Array.from({ length: cards }, (_, index) => (
          <li key={index}>Card {index}</li>
        ))}
      </MediaRow>
    </MemoryRouter>,
  );
}

// jsdom lays nothing out, so the list's scroll geometry is set by hand.
function setGeometry(
  list: HTMLElement,
  geometry: { scrollWidth: number; clientWidth: number; scrollLeft: number },
): void {
  Object.defineProperty(list, "scrollWidth", {
    configurable: true,
    value: geometry.scrollWidth,
  });
  Object.defineProperty(list, "clientWidth", {
    configurable: true,
    value: geometry.clientWidth,
  });
  list.scrollLeft = geometry.scrollLeft;
  fireEvent.scroll(list);
}

function reduceMotion(reduce: boolean): void {
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    writable: true,
    value: (query: string) =>
      ({
        matches: reduce && query === "(prefers-reduced-motion: reduce)",
        media: query,
      }) as MediaQueryList,
  });
}

it("names the row and its list by the heading and links to the full list", () => {
  renderRow();
  expect(screen.getByRole("region", { name: "Trending" })).toBeVisible();
  expect(screen.getByRole("list", { name: "Trending" })).toBeVisible();
  expect(
    screen.getByRole("link", { name: "See all Trending" }),
  ).toHaveAttribute("href", "/browse/trending");
});

it("enables each arrow only when the row can scroll that way", () => {
  renderRow();
  const list = screen.getByRole("list", { name: "Trending" });
  const back = screen.getByRole("button", { name: "Scroll Trending back" });
  const forward = screen.getByRole("button", {
    name: "Scroll Trending forward",
  });
  expect(back).toBeDisabled();
  expect(forward).toBeDisabled();

  setGeometry(list, { scrollWidth: 2000, clientWidth: 800, scrollLeft: 0 });
  expect(back).toBeDisabled();
  expect(forward).toBeEnabled();

  setGeometry(list, { scrollWidth: 2000, clientWidth: 800, scrollLeft: 600 });
  expect(back).toBeEnabled();
  expect(forward).toBeEnabled();

  setGeometry(list, { scrollWidth: 2000, clientWidth: 800, scrollLeft: 1200 });
  expect(back).toBeEnabled();
  expect(forward).toBeDisabled();
});

it("pages one screen less the gutters, smoothly unless motion is reduced", async () => {
  const user = userEvent.setup();
  renderRow();
  const list = screen.getByRole("list", { name: "Trending" });
  list.style.paddingLeft = "32px";
  list.style.paddingRight = "32px";
  const scrollBy = jest.fn<(options?: ScrollToOptions) => void>();
  list.scrollBy = scrollBy as unknown as typeof list.scrollBy;
  setGeometry(list, { scrollWidth: 2000, clientWidth: 800, scrollLeft: 400 });

  reduceMotion(false);
  await user.click(
    screen.getByRole("button", { name: "Scroll Trending forward" }),
  );
  expect(scrollBy).toHaveBeenLastCalledWith({ left: 736, behavior: "smooth" });

  reduceMotion(true);
  await user.click(
    screen.getByRole("button", { name: "Scroll Trending back" }),
  );
  expect(scrollBy).toHaveBeenLastCalledWith({ left: -736, behavior: "auto" });
});

it("shows a status in place of the list and its arrows", () => {
  render(
    <MemoryRouter>
      <MediaRow title="Trending" status={<p>Loading trending…</p>} />
    </MemoryRouter>,
  );
  expect(screen.getByText("Loading trending…")).toBeVisible();
  expect(screen.queryByRole("list")).not.toBeInTheDocument();
  expect(screen.queryByRole("button")).not.toBeInTheDocument();
});
