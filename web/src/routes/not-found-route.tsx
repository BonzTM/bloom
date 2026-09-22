import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { pageTitle, usePageTitle } from "./use-page-title.js";

export function NotFoundRoute(): ReactNode {
  usePageTitle(pageTitle("Page not found"));
  return (
    <>
      <h1>Page not found</h1>
      <p role="alert">The requested page does not exist.</p>
      <Link to="/">Return to home</Link>
    </>
  );
}
