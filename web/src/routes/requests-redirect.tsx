import type { ReactNode } from "react";
import { Navigate, useSearchParams } from "react-router-dom";

// The Discover page used to live at /requests and the search box sent its
// query there. Discovery is the home page now and search has its own page,
// so an old link or bookmark lands on whichever replaced it.
export function RequestsRedirect(): ReactNode {
  const [params] = useSearchParams();
  return <Navigate to={requestsRedirectTarget(params)} replace />;
}

export function requestsRedirectTarget(params: URLSearchParams): string {
  const query = params.get("q");
  if (query === null || query.trim() === "") {
    return "/";
  }
  const next = new URLSearchParams({ q: query });
  const kind = params.get("kind");
  if (kind !== null) {
    next.set("kind", kind);
  }
  return `/search?${next.toString()}`;
}
