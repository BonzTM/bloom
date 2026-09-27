import { useMemo, type ReactNode } from "react";
import { AsyncStatus } from "../components/async-status.js";
import {
  useSession,
  useSessionRecheck,
} from "../features/auth/hooks/auth-queries.js";
import { exportWatchesPath } from "../features/imports/api/imports-api.js";
import { ImportsTable } from "../features/imports/components/imports-table.js";
import { StartImportForm } from "../features/imports/components/start-import-form.js";
import {
  useCancelImport,
  useImports,
  useStartImport,
} from "../features/imports/hooks/imports-queries.js";
import { useMediaServers } from "../features/media-servers/hooks/media-servers-queries.js";
import { SignInNotConfirmed } from "../features/requests/components/list-states.js";
import { accessDenial } from "../lib/api/errors.js";
import { AccessDeniedRoute } from "./access-denied-route.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

export default function ImportsRoute(): ReactNode {
  usePageTitle(pageTitle("Imports"));
  const session = useSession();
  const accountId = session.data?.account.id;
  // The route guard only renders this page for a signed-in account. Keying
  // the page on the account remounts it when the principal changes.
  if (accountId === undefined) {
    return null;
  }
  return <ImportsPage key={accountId} accountId={accountId} />;
}

function ImportsPage({
  accountId,
}: Readonly<{ accountId: string }>): ReactNode {
  const imports = useImports(accountId);
  const servers = useMediaServers(accountId);
  const start = useStartImport(accountId);
  const cancel = useCancelImport(accountId);
  const denial =
    accessDenial(imports.error) ??
    accessDenial(servers.error) ??
    accessDenial(start.error) ??
    accessDenial(cancel.error);
  useSessionRecheck(
    denial !== undefined,
    Math.max(
      imports.errorUpdatedAt,
      servers.errorUpdatedAt,
      start.submittedAt,
      cancel.submittedAt,
    ),
  );
  const serverOptions = useMemo(
    () =>
      (servers.data?.pages ?? []).flatMap((page) =>
        page.items.map((server) => ({ id: server.id, name: server.name })),
      ),
    [servers.data],
  );
  if (denial === "forbidden") {
    return <AccessDeniedRoute />;
  }
  if (denial === "unauthenticated") {
    return (
      <>
        <h1>Imports</h1>
        <SignInNotConfirmed
          noun="imports"
          onRetry={() => Promise.all([imports.refetch(), servers.refetch()])}
        />
      </>
    );
  }
  const serverName = (id: string): string =>
    serverOptions.find((server) => server.id === id)?.name ?? id;
  return (
    <>
      <h1>Imports</h1>
      <p className="page-intro">
        Bring watch history into Bloom from Jellyfin's Playback Reporting plugin
        or from another Bloom's export. A job runs in the background, resumes
        where it left off after a restart, and never records the same source row
        twice.
      </p>
      <section aria-labelledby="start-import-heading" className="card">
        <h2 id="start-import-heading">Start an import</h2>
        {servers.status === "pending" ? (
          <AsyncStatus>Loading servers…</AsyncStatus>
        ) : (
          <StartImportForm
            key={start.submittedAt}
            servers={serverOptions}
            pending={start.isPending}
            serverError={start.error}
            onSubmit={(input) => {
              start.mutate(input);
            }}
          />
        )}
      </section>
      <section aria-labelledby="imports-heading" className="card">
        <h2 id="imports-heading">Import jobs</h2>
        <ImportsTable
          query={imports}
          serverName={serverName}
          cancelling={cancel.isPending ? cancel.variables : undefined}
          actionError={cancel.error}
          onCancel={(id) => {
            cancel.mutate(id);
          }}
        />
      </section>
      <section aria-labelledby="export-heading" className="card">
        <h2 id="export-heading">Export</h2>
        <p>
          Download watches as JSON Lines to move history to another Bloom, or to
          keep a copy. One download holds the newest 10,000 watches; the API
          continues from there with a cursor.
        </p>
        <a href={exportWatchesPath(undefined)} download="bloom-watches.jsonl">
          Download watches
        </a>
      </section>
    </>
  );
}
