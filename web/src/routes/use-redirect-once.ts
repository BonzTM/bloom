import { useEffect, useRef } from "react";
import { useNavigate, type NavigateOptions } from "react-router-dom";

// Navigates once each time `when` turns true. Strict Mode runs effects twice
// in development; the ref makes the second run a no-op. When `when` turns
// false again the hook re-arms, so a later change (a sign-out on a guarded
// page, say) redirects again.
export function useRedirectOnce(
  when: boolean,
  to: string,
  options: NavigateOptions = {},
): void {
  const navigate = useNavigate();
  const done = useRef(false);
  const latest = useRef(options);
  useEffect(() => {
    latest.current = options;
  });
  useEffect(() => {
    if (!when) {
      done.current = false;
      return;
    }
    if (done.current) {
      return;
    }
    done.current = true;
    void navigate(to, latest.current);
  }, [when, to, navigate]);
}
