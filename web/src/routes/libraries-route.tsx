import { type ReactNode } from "react";
import { AsyncStatus } from "../components/async-status.js";
import {
  useSession,
  useSessionRecheck,
} from "../features/auth/hooks/auth-queries.js";
import { hasPermission, permissions } from "../features/auth/permissions.js";
import { LibraryCards } from "../features/catalog/components/library-cards.js";
import {
  useCatalogSummary,
  useLibraries,
  useSyncCatalog,
} from "../features/catalog/hooks/catalog-queries.js";
import { useMediaServers } from "../features/media-servers/hooks/media-servers-queries.js";
import { ReportFailed } from "../features/playback/components/stats-panels.js";
import { accessDenial } from "../lib/api/errors.js";
import { AccessDeniedRoute } from "./access-denied-route.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

const WINDOW_DAYS = 30;

// Every library on every registered server, as cards with what each holds
// and how much of it was watched in the last 30 days.
export default function LibrariesRoute(): ReactNode {
  usePageTitle(pageTitle("Libraries"));
  const session = useSession();
  const accountId = session.data?.account.id;
  const canSync =
    session.data !== undefined &&
    session.data !== null &&
    hasPermission(session.data.permissions, permissions.adminSettings);
  if (accountId === undefined) {
    return null;
  }
  return (
    <LibrariesPage key={accountId} accountId={accountId} canSync={canSync} />
  );
}

function LibrariesPage({
  accountId,
  canSync,
}: Readonly<{ accountId: string; canSync: boolean }>): ReactNode {
  const servers = useMediaServers(accountId);
  const denial = accessDenial(servers.error);
  useSessionRecheck(denial !== undefined, servers.errorUpdatedAt);
  if (denial === "forbidden") {
    return <AccessDeniedRoute />;
  }
  const items = (servers.data?.pages ?? []).flatMap((page) => page.items);
  return (
    <>
      <h1>Libraries</h1>
      <p className="page-intro">
        What each library holds and how much of it people watch. Bloom walks
        every library on a schedule; a card shows counts once the first walk has
        finished.
      </p>
      {servers.status === "pending" ? (
        <AsyncStatus>Loading servers…</AsyncStatus>
      ) : servers.status === "error" ? (
        <ReportFailed what="The servers" onRetry={servers.refetch} />
      ) : items.length === 0 ? (
        <p>No media server is registered yet.</p>
      ) : (
        items.map((server) => (
          <ServerLibraries
            key={server.id}
            accountId={accountId}
            serverId={server.id}
            serverName={server.name}
            canSync={canSync}
          />
        ))
      )}
    </>
  );
}

function ServerLibraries({
  accountId,
  serverId,
  serverName,
  canSync,
}: Readonly<{
  accountId: string;
  serverId: string;
  serverName: string;
  canSync: boolean;
}>): ReactNode {
  const libraries = useLibraries(accountId, serverId);
  const summary = useCatalogSummary(accountId, serverId, WINDOW_DAYS);
  const sync = useSyncCatalog(accountId);
  const headingId = `libraries-${serverId}`;
  return (
    <section aria-labelledby={headingId} className="card">
      <div className="section-head">
        <h2 id={headingId}>{serverName}</h2>
        {canSync ? (
          <button
            type="button"
            disabled={sync.isPending}
            onClick={() => {
              sync.mutate(serverId);
            }}
          >
            {sync.isPending ? "Asking…" : "Sync now"}
            <span className="visually-hidden"> {serverName}</span>
          </button>
        ) : null}
      </div>
      {sync.isSuccess ? (
        <p role="status">A full walk of {serverName} is queued.</p>
      ) : null}
      {sync.isError ? (
        <p role="alert">
          The walk could not be queued; one may already be running.
        </p>
      ) : null}
      {libraries.status === "pending" || summary.status === "pending" ? (
        <AsyncStatus>Loading libraries…</AsyncStatus>
      ) : libraries.status === "error" ? (
        <ReportFailed what="The libraries" onRetry={libraries.refetch} />
      ) : summary.status === "error" ? (
        <ReportFailed what="The catalog" onRetry={summary.refetch} />
      ) : (
        <LibraryCards
          serverId={serverId}
          serverName={serverName}
          libraries={libraries.data}
          summaries={summary.data}
        />
      )}
    </section>
  );
}
