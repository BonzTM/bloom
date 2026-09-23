import { expect, it } from "@jest/globals";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import {
  envelope,
  jsonApi,
  mockSession,
  setMockPermissions,
  signInMockSession,
} from "../mocks/handlers.js";
import { createGate } from "../test/gate.js";
import { renderApp } from "../test/render-app.js";
import { server } from "../test/server.js";

async function signInFromLoginPage(user: ReturnType<typeof userEvent.setup>) {
  await screen.findByRole("heading", { name: "Sign in", level: 1 });
  await user.type(screen.getByLabelText("Username"), "admin");
  await user.type(screen.getByLabelText("Password"), "correct horse");
  await user.click(screen.getByRole("button", { name: "Sign in" }));
}

it("hides the admin link from a signed-out visitor", async () => {
  renderApp();
  expect(await screen.findByRole("link", { name: "Sign in" })).toBeVisible();
  expect(screen.queryByRole("link", { name: "Admin" })).not.toBeInTheDocument();
});

it("hides the admin link from an account without admin permissions", async () => {
  setMockPermissions(["users.read"]);
  signInMockSession();
  renderApp();
  expect(await screen.findByText("Signed in as admin")).toBeVisible();
  expect(screen.queryByRole("link", { name: "Admin" })).not.toBeInTheDocument();
});

it("takes an administrator from the navigation to the roles page", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp();

  await user.click(await screen.findByRole("link", { name: "Admin" }));
  expect(
    await screen.findByRole("heading", { name: "Administration", level: 1 }),
  ).toBeVisible();
  expect(document.title).toBe("Administration | Bloom");

  await user.click(screen.getByRole("link", { name: "Roles" }));
  expect(
    await screen.findByRole("heading", { name: "Roles", level: 1 }),
  ).toBeVisible();
  expect(document.title).toBe("Roles | Bloom");
  expect(screen.getByRole("link", { name: "Admin" })).toHaveAttribute(
    "aria-current",
    "page",
  );
});

it("lists the roles with their kind and permissions", async () => {
  signInMockSession();
  renderApp("/admin/roles");

  const table = await screen.findByRole("table", {
    name: "Roles, ordered by name",
  });
  const admin = within(table).getByRole("row", { name: /^admin / });
  expect(within(admin).getByRole("rowheader")).toHaveTextContent("admin");
  expect(admin).toHaveTextContent("Built-in");
  expect(
    within(admin).getByRole("list", { name: "Permissions of admin" }),
  ).toHaveTextContent("admin.roles");
  const custom = within(table).getByRole("row", { name: /^custom-01 / });
  expect(custom).toHaveTextContent("Custom");
  expect(
    within(custom).getByRole("list", { name: "Permissions of custom-01" }),
  ).toHaveTextContent("requests.read.own");
  expect(within(table).getAllByRole("rowheader")).toHaveLength(50);
  expect(
    within(table).queryByRole("rowheader", { name: "member" }),
  ).not.toBeInTheDocument();
});

it("shows the empty state when there are no roles", async () => {
  server.use(
    http.get(
      "*/api/v1/roles",
      jsonApi(() => HttpResponse.json({ items: [], next_cursor: "" })),
    ),
  );
  signInMockSession();
  renderApp("/admin/roles");

  expect(await screen.findByText("There are no roles yet.")).toBeVisible();
  expect(screen.queryByRole("table")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "Load more roles" }),
  ).not.toBeInTheDocument();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

it("loads the next page of roles on request and then offers no more", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/admin/roles");
  await screen.findByRole("rowheader", { name: "admin" });

  await user.click(screen.getByRole("button", { name: "Load more roles" }));

  const member = await screen.findByRole("row", { name: /^member / });
  expect(member).toHaveTextContent("Built-in");
  expect(screen.getByRole("row", { name: /^reviewer / })).toHaveTextContent(
    "Custom",
  );
  expect(screen.getAllByRole("rowheader")).toHaveLength(52);
  expect(screen.getByRole("rowheader", { name: "admin" })).toBeVisible();
  expect(
    screen.queryByRole("button", { name: "Load more roles" }),
  ).not.toBeInTheDocument();
});

it("shows nothing protected while the session check is pending, then the page", async () => {
  const gate = createGate();
  signInMockSession();
  server.use(
    http.get(
      "*/api/v1/auth/me",
      jsonApi(async () => {
        await gate.wait;
        return HttpResponse.json(mockSession);
      }),
    ),
  );
  renderApp("/admin/roles");

  expect(await screen.findByText("Checking sign-in…")).toHaveRole("status");
  expect(screen.getAllByRole("status")).toHaveLength(1);
  expect(screen.getByRole("main")).toBeEmptyDOMElement();
  expect(
    screen.queryByRole("heading", { name: "Roles", level: 1 }),
  ).not.toBeInTheDocument();

  gate.open();
  expect(await screen.findByRole("rowheader", { name: "admin" })).toBeVisible();
});

it("offers one retry from the navigation when the session check fails", async () => {
  const user = userEvent.setup();
  let failing = true;
  server.use(
    http.get(
      "*/api/v1/auth/me",
      jsonApi(() =>
        failing
          ? new HttpResponse("upstream down", { status: 502 })
          : undefined,
      ),
    ),
  );
  signInMockSession();
  renderApp("/admin/roles");

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Sign-in status is unavailable.",
  );
  expect(screen.getAllByRole("alert")).toHaveLength(1);
  expect(screen.getAllByRole("button", { name: "Retry" })).toHaveLength(1);
  expect(screen.getByRole("main")).toHaveTextContent(
    "This page cannot be shown until your sign-in status is known.",
  );
  expect(
    screen.queryByRole("heading", { name: "Roles", level: 1 }),
  ).not.toBeInTheDocument();

  failing = false;
  await user.click(screen.getByRole("button", { name: "Retry" }));
  expect(await screen.findByRole("rowheader", { name: "admin" })).toBeVisible();
});

it("sends a signed-out visitor to sign in exactly once and back to the full destination", async () => {
  const user = userEvent.setup();
  // The session answer is held until the arrival counter is listening, so
  // a redirect can neither happen before the subscription nor be missed.
  const gate = createGate();
  server.use(
    http.get(
      "*/api/v1/auth/me",
      jsonApi(async () => {
        await gate.wait;
        return undefined;
      }),
    ),
  );
  const { router } = renderApp("/admin/roles?tab=custom#reviewer");
  let loginArrivals = 0;
  let previous = router.state.location.key;
  const unsubscribe = router.subscribe((state) => {
    if (state.location.key !== previous) {
      previous = state.location.key;
      if (state.location.pathname === "/login") {
        loginArrivals += 1;
      }
    }
  });
  gate.open();

  await signInFromLoginPage(user);

  expect(
    await screen.findByRole("heading", { name: "Roles", level: 1 }),
  ).toBeVisible();
  expect(await screen.findByRole("rowheader", { name: "admin" })).toBeVisible();
  expect(router.state.location.pathname).toBe("/admin/roles");
  expect(router.state.location.search).toBe("?tab=custom");
  expect(router.state.location.hash).toBe("#reviewer");
  expect(loginArrivals).toBe(1);
  unsubscribe();
});

it("refuses the roles page to an administrator without the roles permission", async () => {
  setMockPermissions(["admin.settings"]);
  signInMockSession();
  renderApp("/admin/roles");

  expect(
    await screen.findByRole("heading", { name: "Access denied", level: 1 }),
  ).toBeVisible();
  expect(screen.getByRole("alert")).toHaveTextContent(
    "Your account does not have permission to view this page.",
  );
  expect(document.title).toBe("Access denied | Bloom");
  expect(screen.getByRole("link", { name: "Admin" })).toBeVisible();
  expect(screen.queryByRole("table")).not.toBeInTheDocument();
});

it("refuses the whole admin area to an account with no admin permission", async () => {
  setMockPermissions(["users.read"]);
  signInMockSession();
  renderApp("/admin");

  expect(
    await screen.findByRole("heading", { name: "Access denied", level: 1 }),
  ).toBeVisible();
  expect(screen.getByRole("link", { name: "Return to home" })).toBeVisible();
});

it("hides the roles section from the administration page without the roles permission", async () => {
  setMockPermissions(["admin.settings"]);
  signInMockSession();
  renderApp("/admin");

  await screen.findByRole("heading", { name: "Administration", level: 1 });
  expect(screen.queryByRole("link", { name: "Roles" })).not.toBeInTheDocument();
});

it("shows the refusal page when the server forbids the roles request", async () => {
  server.use(
    http.get(
      "*/api/v1/roles",
      jsonApi(() => envelope(403, "forbidden", "missing permission")),
    ),
  );
  signInMockSession();
  renderApp("/admin/roles");

  expect(
    await screen.findByRole("heading", { name: "Access denied", level: 1 }),
  ).toBeVisible();
  expect(screen.queryByRole("table")).not.toBeInTheDocument();
});

it("sends the person to sign in when the server says the session is gone", async () => {
  let sessionGone = false;
  server.use(
    http.get(
      "*/api/v1/roles",
      jsonApi(() => {
        sessionGone = true;
        return envelope(401, "unauthorized", "sign in required");
      }),
    ),
    http.get(
      "*/api/v1/auth/me",
      jsonApi(() =>
        sessionGone
          ? envelope(401, "unauthorized", "sign in required")
          : undefined,
      ),
    ),
  );
  signInMockSession();
  const { router } = renderApp("/admin/roles");

  expect(
    await screen.findByRole("heading", { name: "Sign in", level: 1 }),
  ).toBeVisible();
  expect(router.state.location.state).toEqual({ from: "/admin/roles" });
});

it("keeps the page with a retry when the server contradicts itself about the session", async () => {
  const user = userEvent.setup();
  let failing = true;
  server.use(
    http.get(
      "*/api/v1/roles",
      jsonApi(() =>
        failing ? envelope(401, "unauthorized", "sign in required") : undefined,
      ),
    ),
  );
  signInMockSession();
  renderApp("/admin/roles");

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "your sign-in could not be confirmed",
  );
  failing = false;
  await user.click(screen.getByRole("button", { name: "Retry" }));

  expect(await screen.findByRole("rowheader", { name: "admin" })).toBeVisible();
});

it("reports a roles loading failure and recovers on retry", async () => {
  const user = userEvent.setup();
  // A flag, not a request count: Strict Mode mounts twice and the first
  // fetch is cancelled, so counting requests would be fragile.
  let failing = true;
  server.use(
    http.get(
      "*/api/v1/roles",
      jsonApi(() =>
        failing ? envelope(500, "internal", "internal error") : undefined,
      ),
    ),
  );
  signInMockSession();
  renderApp("/admin/roles");

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "The roles could not be loaded.",
  );
  failing = false;
  await user.click(screen.getByRole("button", { name: "Retry" }));

  expect(await screen.findByRole("rowheader", { name: "admin" })).toBeVisible();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

it("keeps the loaded roles and warns when a refresh fails", async () => {
  const user = userEvent.setup();
  signInMockSession();
  const { queryClient } = renderApp("/admin/roles");
  await screen.findByRole("rowheader", { name: "admin" });

  let failing = true;
  server.use(
    http.get(
      "*/api/v1/roles",
      jsonApi(() =>
        failing ? envelope(500, "internal", "internal error") : undefined,
      ),
    ),
  );
  await queryClient.refetchQueries({ queryKey: ["roles"] });

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "The roles could not be refreshed.",
  );
  expect(screen.getByRole("rowheader", { name: "admin" })).toBeVisible();

  failing = false;
  await user.click(screen.getByRole("button", { name: "Retry" }));
  expect(
    await screen.findByText(
      (_content, element) =>
        element?.tagName === "BODY" &&
        !element.textContent.includes("could not be refreshed"),
    ),
  ).toBeInTheDocument();
  expect(screen.getByRole("rowheader", { name: "admin" })).toBeVisible();
});

it("keeps the loaded roles and reports when the next page fails", async () => {
  const user = userEvent.setup();
  server.use(
    http.get(
      "*/api/v1/roles",
      jsonApi(({ request }) =>
        new URL(request.url).searchParams.has("cursor")
          ? envelope(500, "internal", "internal error")
          : undefined,
      ),
    ),
  );
  signInMockSession();
  renderApp("/admin/roles");
  await screen.findByRole("rowheader", { name: "admin" });

  await user.click(screen.getByRole("button", { name: "Load more roles" }));

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "More roles could not be loaded.",
  );
  expect(screen.getByRole("rowheader", { name: "admin" })).toBeVisible();
  expect(screen.getByRole("button", { name: "Load more roles" })).toBeEnabled();
});

it("treats a malformed roles response as a failure", async () => {
  server.use(
    http.get(
      "*/api/v1/roles",
      jsonApi(() => Response.json({ items: [{ id: "only-an-id" }] })),
    ),
  );
  signInMockSession();
  renderApp("/admin/roles");

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "The roles could not be loaded.",
  );
});

it("drops the cached roles when the person signs out", async () => {
  const user = userEvent.setup();
  signInMockSession();
  const { queryClient } = renderApp("/admin/roles");
  await screen.findByRole("rowheader", { name: "admin" });
  expect(
    queryClient.getQueryCache().findAll({ queryKey: ["roles"] }),
  ).toHaveLength(1);

  await user.click(screen.getByRole("button", { name: "Sign out" }));

  await screen.findByRole("heading", { name: "Sign in", level: 1 });
  expect(
    queryClient.getQueryCache().findAll({ queryKey: ["roles"] }),
  ).toHaveLength(0);
});
