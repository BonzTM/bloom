import { expect, it } from "@jest/globals";
import { renderHook } from "@testing-library/react";
import { StrictMode, type ReactNode } from "react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { useRedirectOnce } from "./use-redirect-once.js";

type Props = Readonly<{ when: boolean; to: string }>;

// Strict Mode, as production renders: effects run twice on mount, so a hook
// that navigated on every effect run would arrive twice.
function wrapper({ children }: Readonly<{ children: ReactNode }>) {
  return (
    <StrictMode>
      <MemoryRouter initialEntries={["/start"]}>{children}</MemoryRouter>
    </StrictMode>
  );
}

function useHarness({ when, to }: Props) {
  useRedirectOnce(when, to, { replace: true, state: { from: "/start" } });
  return useLocation();
}

function arrivals(keys: readonly string[]): number {
  return new Set(keys).size;
}

it("navigates once when the condition is true, with the options given", () => {
  const seen: string[] = [];
  const { result, rerender } = renderHook(
    (props: Props) => {
      const location = useHarness(props);
      if (location.pathname === "/login") {
        seen.push(location.key);
      }
      return location;
    },
    { wrapper, initialProps: { when: true, to: "/login" } },
  );

  expect(result.current.pathname).toBe("/login");
  expect(result.current.state).toEqual({ from: "/start" });
  rerender({ when: true, to: "/login" });
  rerender({ when: true, to: "/login" });
  expect(arrivals(seen)).toBe(1);
});

it("does nothing while the condition is false, then fires when it turns true", () => {
  const { result, rerender } = renderHook(useHarness, {
    wrapper,
    initialProps: { when: false, to: "/login" },
  });
  expect(result.current.pathname).toBe("/start");

  rerender({ when: true, to: "/login" });
  expect(result.current.pathname).toBe("/login");
});

it("re-arms after the condition clears, so a later change redirects again", () => {
  const seen: string[] = [];
  const { rerender } = renderHook(
    (props: Props) => {
      const location = useHarness(props);
      if (location.pathname === "/login") {
        seen.push(location.key);
      }
      return location;
    },
    { wrapper, initialProps: { when: true, to: "/login" } },
  );
  expect(arrivals(seen)).toBe(1);

  rerender({ when: false, to: "/login" });
  rerender({ when: true, to: "/login" });
  expect(arrivals(seen)).toBe(2);
});

it("does not navigate again for a changed destination while the condition holds", () => {
  const { result, rerender } = renderHook(useHarness, {
    wrapper,
    initialProps: { when: true, to: "/login" },
  });
  expect(result.current.pathname).toBe("/login");

  rerender({ when: true, to: "/elsewhere" });
  expect(result.current.pathname).toBe("/login");
});
