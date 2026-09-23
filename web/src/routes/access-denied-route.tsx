import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { pageTitle, usePageTitle } from "./use-page-title.js";

export function AccessDeniedRoute(): ReactNode {
  usePageTitle(pageTitle("Access denied"));
  return (
    <>
      <h1>Access denied</h1>
      <p role="alert">
        Your account does not have permission to view this page.
      </p>
      <Link to="/">Return to home</Link>
    </>
  );
}
