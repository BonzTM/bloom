import { expect, it } from "@jest/globals";
import { screen, within } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { envelope, jsonApi } from "../../../mocks/handlers.js";
import { renderApp } from "../../../test/render-app.js";
import { server } from "../../../test/server.js";

it("shows a recoverable alert when the version endpoint fails", async () => {
  server.use(
    http.get(
      "*/api/v1/version",
      jsonApi(() => envelope(503, "unavailable", "not ready")),
    ),
  );

  renderApp("/about");

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "The server version could not be loaded",
  );
});

it("rejects a version payload from a different product", async () => {
  server.use(
    http.get(
      "*/api/v1/version",
      jsonApi(() =>
        HttpResponse.json({ name: "other", version: "1.0.0", commit: "abc" }),
      ),
    ),
  );

  renderApp("/about");

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "The server version could not be loaded",
  );
});

it("labels an empty commit as unknown", async () => {
  server.use(
    http.get(
      "*/api/v1/version",
      jsonApi(() =>
        HttpResponse.json({ name: "bloom", version: "0.2.0", commit: "" }),
      ),
    ),
  );

  renderApp("/about");

  const main = within(screen.getByRole("main"));
  expect(await main.findByText(/Version 0\.2\.0/)).toBeVisible();
  expect(main.getByText("unknown")).toBeVisible();
});
