import { useEffect, type RefObject } from "react";
import { useLocation } from "react-router-dom";

// After client-side navigation the browser does not move focus, so keyboard
// and screen-reader users would be left wherever the previous page put them.
// Focusing the main region on every location change gives them a fresh start.
export function useRouteFocus(main: RefObject<HTMLElement | null>): void {
  const location = useLocation();
  useEffect(() => {
    main.current?.focus();
  }, [location.key, main]);
}
