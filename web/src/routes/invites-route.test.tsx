import { expect, it } from "@jest/globals";
import type { QueryClient } from "@tanstack/react-query";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import {
  envelope,
  jsonApi,
  setMockPermissions,
  signInMockSession,
} from "../mocks/handlers.js";
import { renderApp, type AppRender } from "../test/render-app.js";
import { server } from "../test/server.js";

type User = ReturnType<typeof userEvent.setup>;

async function openInvites(): Promise<AppRender & { table: HTMLElement }> {
  signInMockSession();
  const rendered = renderApp("/admin/invites");
  const table = await screen.findByRole("table", {
    name: "Invites, newest first",
  });
  return { ...rendered, table };
}

async function fillInvite(user: User, label: string): Promise<void> {
  await user.selectOptions(screen.getByLabelText("Media server"), "Cabin");
  await user.type(screen.getByLabelText("Label"), label);
}

function cachedMutationText(queryClient: QueryClient): string {
  return JSON.stringify(
    queryClient
      .getMutationCache()
      .getAll()
      .map((mutation) => mutation.state),
  );
}

it("offers the invites page from the administration index", async () => {
  const user = userEvent.setup();
  signInMockSession();
  renderApp("/admin");
  await user.click(await screen.findByRole("link", { name: "Invites" }));
  expect(
    await screen.findByRole("heading", { name: "Invites", level: 1 }),
  ).toBeVisible();
  expect(document.title).toBe("Invites | Bloom");
});

it("hides the invites link without users.invite", async () => {
  setMockPermissions(["admin.roles"]);
  signInMockSession();
  renderApp("/admin");
  expect(await screen.findByRole("link", { name: "Roles" })).toBeVisible();
  expect(
    screen.queryByRole("link", { name: "Invites" }),
  ).not.toBeInTheDocument();
});

it("lists invites with their server, status, uses, and dates", async () => {
  const { table } = await openInvites();
  const family = within(table).getByRole("row", { name: /^Family / });
  expect(family).toHaveTextContent("Cabin");
  expect(family).toHaveTextContent("Active");
  expect(family).toHaveTextContent("1 of 5");
  expect(family).toHaveTextContent("2026-12-31");
  expect(
    within(family).getByRole("button", { name: "Revoke Family" }),
  ).toBeVisible();
  const old = within(table).getByRole("row", { name: /^Old link / });
  expect(old).toHaveTextContent("Revoked");
  expect(old).toHaveTextContent("2, no limit");
  expect(old).toHaveTextContent("Never");
  expect(within(old).queryByRole("button")).not.toBeInTheDocument();
  expect(within(table).getAllByRole("rowheader")[0]).toHaveTextContent(
    "Family",
  );
});

it("creates an invite, shows its link once, and keeps the code out of the cache", async () => {
  const user = userEvent.setup();
  const { queryClient } = await openInvites();
  await fillInvite(user, "Friends");
  await user.type(screen.getByLabelText("Use limit"), "2");

  await user.click(screen.getByRole("button", { name: "Create invite" }));

  const heading = await screen.findByRole("heading", {
    name: "Invite Friends is ready",
    level: 3,
  });
  expect(heading).toHaveFocus();
  const link = screen.getByText(/\/invite\/ABCDEFGHIJKLMNOPQRSTUVWXAA$/);
  expect(link).toHaveTextContent(
    "http://localhost/invite/ABCDEFGHIJKLMNOPQRSTUVWXAA",
  );
  expect(cachedMutationText(queryClient)).not.toContain(
    "ABCDEFGHIJKLMNOPQRSTUVWXAA",
  );
  expect(
    await screen.findByRole("rowheader", { name: "Friends" }),
  ).toBeVisible();
  expect(screen.getByRole("row", { name: /^Friends / })).toHaveTextContent(
    "0 of 2",
  );
  expect(screen.getByLabelText("Label")).toHaveValue("");

  await user.click(screen.getByRole("button", { name: "Copy link" }));
  expect(await screen.findByText("Link copied.")).toBeVisible();
  await user.click(screen.getByRole("button", { name: "Dismiss" }));
  expect(screen.queryByText(/\/invite\//)).not.toBeInTheDocument();
});

it("explains what is missing before sending anything", async () => {
  const user = userEvent.setup();
  await openInvites();

  await user.click(screen.getByRole("button", { name: "Create invite" }));

  const serverField = screen.getByLabelText("Media server");
  expect(serverField).toBeInvalid();
  expect(serverField).toHaveFocus();
  expect(serverField).toHaveAccessibleDescription(
    "Choose the server this invite is for.",
  );
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

it("reports a server that disappeared", async () => {
  const user = userEvent.setup();
  await openInvites();
  server.use(
    http.post(
      "*/api/v1/invites",
      jsonApi(() => envelope(404, "not_found", "media server not found")),
    ),
  );
  await fillInvite(user, "Attic");

  await user.click(screen.getByRole("button", { name: "Create invite" }));

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "That media server no longer exists.",
  );
  expect(screen.getByRole("alert")).toHaveFocus();
});

it("revokes an invite after an in-row confirmation", async () => {
  const user = userEvent.setup();
  const { table } = await openInvites();

  await user.click(
    within(table).getByRole("button", { name: "Revoke Family" }),
  );
  expect(
    within(table).getByRole("button", { name: "Confirm revoking Family" }),
  ).toHaveFocus();
  await user.click(
    within(table).getByRole("button", { name: "Cancel revoking Family" }),
  );
  expect(
    within(table).getByRole("button", { name: "Revoke Family" }),
  ).toHaveFocus();

  await user.click(
    within(table).getByRole("button", { name: "Revoke Family" }),
  );
  await user.click(
    within(table).getByRole("button", { name: "Confirm revoking Family" }),
  );

  const family = await within(table).findByRole("row", {
    name: /^Family .*Revoked/,
  });
  expect(family).toHaveTextContent("Revoked");
  expect(within(family).queryByRole("button")).not.toBeInTheDocument();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

it("lets an inviter without admin.settings choose a server", async () => {
  setMockPermissions(["users.invite"]);
  await openInvites();
  const select = await screen.findByLabelText("Media server");
  expect(within(select).getByRole("option", { name: "Cabin" })).toBeVisible();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

it("offers a retry when the servers cannot be listed", async () => {
  const user = userEvent.setup();
  let failing = true;
  server.use(
    http.get(
      "*/api/v1/invites/servers",
      jsonApi(() => (failing ? envelope(500, "internal", "boom") : undefined)),
    ),
  );
  await openInvites();
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "The media servers could not be loaded",
  );
  failing = false;
  await user.click(screen.getByRole("button", { name: "Retry" }));
  expect(await screen.findByLabelText("Media server")).toBeVisible();
});

it("focuses the explanation when a revocation fails", async () => {
  const user = userEvent.setup();
  const { table } = await openInvites();
  server.use(
    http.delete(
      "*/api/v1/invites/:id",
      jsonApi(() => envelope(404, "not_found", "invite not found")),
    ),
  );
  await user.click(
    within(table).getByRole("button", { name: "Revoke Family" }),
  );
  await user.click(
    within(table).getByRole("button", { name: "Confirm revoking Family" }),
  );
  const alert = await screen.findByRole("alert");
  expect(alert).toHaveTextContent("That invite was already gone.");
  expect(alert).toHaveFocus();
});

it("sends one request when the form is submitted twice", async () => {
  const user = userEvent.setup();
  let requests = 0;
  server.use(
    http.post(
      "*/api/v1/invites",
      jsonApi(() => {
        requests += 1;
        return undefined;
      }),
    ),
  );
  await openInvites();
  await fillInvite(user, "Twice");
  const submit = screen.getByRole("button", { name: "Create invite" });

  await user.dblClick(submit);

  expect(
    await screen.findByRole("heading", { name: "Invite Twice is ready" }),
  ).toBeVisible();
  expect(requests).toBe(1);
  expect(screen.getAllByText(/\/invite\//)).toHaveLength(1);
});

it("stops paging servers at a failed later page and resumes on retry", async () => {
  const user = userEvent.setup();
  let secondPageFails = true;
  server.use(
    http.get(
      "*/api/v1/invites/servers",
      jsonApi(({ request }) => {
        const cursor = new URL(request.url).searchParams.get("cursor");
        if (cursor === null) {
          return undefined;
        }
        if (secondPageFails) {
          return envelope(500, "internal", "boom");
        }
        return HttpResponse.json({ items: [], next_cursor: "" });
      }),
    ),
    http.get(
      "*/api/v1/invites/servers",
      jsonApi(({ request }) =>
        new URL(request.url).searchParams.has("cursor")
          ? undefined
          : HttpResponse.json({
              items: [],
              next_cursor: "offset:2",
            }),
      ),
    ),
  );
  await openInvites();
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "The media servers could not be loaded",
  );
  secondPageFails = false;
  await user.click(screen.getByRole("button", { name: "Retry" }));
  expect(
    await screen.findByRole("link", { name: "Register one first." }),
  ).toBeVisible();
});

it("points at registration when no server exists yet", async () => {
  server.use(
    http.get(
      "*/api/v1/invites/servers",
      jsonApi(() => HttpResponse.json({ items: [], next_cursor: "" })),
    ),
  );
  await openInvites();
  expect(
    await screen.findByRole("link", { name: "Register one first." }),
  ).toHaveAttribute("href", "/admin/media-servers");
});
