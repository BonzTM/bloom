import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { AsyncStatus } from "../components/async-status.js";
import {
  useSession,
  useSessionRecheck,
} from "../features/auth/hooks/auth-queries.js";
import type { CreatedInvite } from "../features/invites/api/invites-schemas.js";
import {
  CreateInviteForm,
  type ServerChoice,
} from "../features/invites/components/create-invite-form.js";
import { InviteLink } from "../features/invites/components/invite-link.js";
import { InvitesTable } from "../features/invites/components/invites-table.js";
import {
  useCreateInvite,
  useInvites,
  useInviteServers,
  useRevokeInvite,
} from "../features/invites/hooks/invites-queries.js";
import { accessDenial } from "../lib/api/errors.js";
import { AccessDeniedRoute } from "./access-denied-route.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

export default function InvitesRoute(): ReactNode {
  usePageTitle(pageTitle("Invites"));
  const session = useSession();
  const accountId = session.data?.account.id;
  // The route guard only renders this page for a signed-in account. Keying
  // the page on the account remounts it when the principal changes, so a
  // late answer to one account's action can never show under another's.
  if (accountId === undefined) {
    return null;
  }
  return <InvitesPage key={accountId} accountId={accountId} />;
}

function InvitesPage({
  accountId,
}: Readonly<{ accountId: string }>): ReactNode {
  const invites = useInvites(accountId);
  const servers = useInviteServers(accountId);
  const create = useCreateInvite(accountId);
  const revoke = useRevokeInvite(accountId);
  const denial =
    accessDenial(invites.error) ??
    accessDenial(servers.error) ??
    accessDenial(create.error) ??
    accessDenial(revoke.error);
  // The server denied something the cached session says is allowed: the
  // session is gone, or a permission was taken away. Re-reading the session
  // lets the route guard send the person to sign in or off this page.
  useSessionRecheck(
    denial !== undefined,
    Math.max(
      invites.errorUpdatedAt,
      servers.errorUpdatedAt,
      create.submittedAt,
      revoke.submittedAt,
    ),
  );
  // Every registered server is a valid choice, so every page is fetched,
  // one at a time, stopping at a failure (the retry below resumes) and at a
  // page bound no real installation reaches.
  const {
    hasNextPage,
    isFetchingNextPage,
    isFetchNextPageError,
    fetchNextPage,
  } = servers;
  const pageCount = servers.data?.pages.length ?? 0;
  useEffect(() => {
    if (
      hasNextPage &&
      !isFetchingNextPage &&
      !isFetchNextPageError &&
      pageCount < MAX_SERVER_PAGES
    ) {
      void fetchNextPage();
    }
  }, [
    hasNextPage,
    isFetchingNextPage,
    isFetchNextPageError,
    pageCount,
    fetchNextPage,
  ]);
  const serverChoices = useMemo(() => serverList(servers.data), [servers.data]);
  const serverNames = useMemo(
    () => new Map(serverChoices.map((server) => [server.id, server.name])),
    [serverChoices],
  );
  if (denial === "forbidden") {
    return <AccessDeniedRoute />;
  }
  return (
    <>
      <h1>Invites</h1>
      <p className="page-intro">
        An invite link lets someone create their own account on one of your
        media servers. Each link is shown once, can expire, and can be limited
        to a number of uses.
      </p>
      <CreateSection
        create={create}
        servers={serverChoices}
        serversState={serverListState(servers)}
        onRetryServers={
          servers.isFetchNextPageError ? servers.fetchNextPage : servers.refetch
        }
      />
      <section aria-labelledby="invites-heading" className="card">
        <h2 id="invites-heading">Invites</h2>
        {denial === "unauthenticated" ? (
          <SignInNotConfirmed onRetry={invites.refetch} />
        ) : (
          <InvitesTable
            query={invites}
            serverNames={serverNames}
            revoking={revoke.isPending ? revoke.variables : undefined}
            revokeError={revoke.error}
            onRevoke={(id) => {
              revoke.mutate(id);
            }}
          />
        )}
      </section>
    </>
  );
}

function serverList(
  data: ReturnType<typeof useInviteServers>["data"],
): readonly ServerChoice[] {
  if (data === undefined) {
    return [];
  }
  return data.pages.flatMap((page) =>
    page.items.map((server) => ({ id: server.id, name: server.name })),
  );
}

type ServerListState = "loading" | "failed" | "empty" | "ready";

const MAX_SERVER_PAGES = 20;

// The servers a new invite may name come from the inviter's own server
// list; each state it can be in is shown as itself rather than as an
// empty list. A refusal is handled by the page's session recheck.
function serverListState(
  servers: ReturnType<typeof useInviteServers>,
): ServerListState {
  if (
    (servers.status === "error" && servers.data === undefined) ||
    servers.isFetchNextPageError
  ) {
    return "failed";
  }
  if (servers.status === "pending" || servers.hasNextPage) {
    return "loading";
  }
  return serverList(servers.data).length === 0 ? "empty" : "ready";
}

type CreateSectionProps = Readonly<{
  create: ReturnType<typeof useCreateInvite>;
  servers: readonly ServerChoice[];
  serversState: ServerListState;
  onRetryServers: () => Promise<unknown>;
}>;

function CreateSection({
  create,
  servers,
  serversState,
  onRetryServers,
}: CreateSectionProps): ReactNode {
  const [created, setCreated] = useState<CreatedInvite | null>(null);
  // Remounting the form after a success clears it for the next invite.
  const [formKey, setFormKey] = useState(0);
  return (
    <section aria-labelledby="create-invite-heading" className="card">
      <h2 id="create-invite-heading">Create an invite</h2>
      {serversState === "loading" ? (
        <AsyncStatus>Loading media servers…</AsyncStatus>
      ) : serversState === "failed" ? (
        <>
          <AsyncStatus kind="alert">
            The media servers could not be loaded, so no server can be chosen
            yet.
          </AsyncStatus>
          <button
            type="button"
            onClick={() => {
              void onRetryServers();
            }}
          >
            Retry
          </button>
        </>
      ) : serversState === "empty" ? (
        <p>
          No media server is registered yet.{" "}
          <Link to="/admin/media-servers">Register one first.</Link>
        </p>
      ) : (
        <CreateInviteForm
          key={formKey}
          servers={servers}
          pending={create.isPending}
          serverError={create.error}
          now={() => new Date()}
          onSubmit={(input) => {
            setCreated(null);
            create.create(input, {
              onCreated: (result) => {
                setCreated(result);
                setFormKey((current) => current + 1);
              },
            });
          }}
        />
      )}
      {created === null ? null : (
        <InviteLink
          created={created}
          origin={window.location.origin}
          onDismiss={() => {
            setCreated(null);
          }}
        />
      )}
    </section>
  );
}

function SignInNotConfirmed({
  onRetry,
}: Readonly<{ onRetry: () => Promise<unknown> }>): ReactNode {
  return (
    <>
      <AsyncStatus kind="alert">
        The invites could not be loaded because your sign-in could not be
        confirmed.
      </AsyncStatus>
      <button
        type="button"
        onClick={() => {
          void onRetry();
        }}
      >
        Retry
      </button>
    </>
  );
}
