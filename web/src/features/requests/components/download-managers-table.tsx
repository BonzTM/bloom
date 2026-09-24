import type { ReactNode } from "react";
import { AsyncStatus } from "../../../components/async-status.js";
import type { DownloadManager } from "../api/download-manager-schemas.js";
import type { useDownloadManagers } from "../hooks/requests-queries.js";
import { ConfirmControls } from "./confirm-controls.js";
import {
  ActionFailed,
  LoadFailed,
  Pager,
  RefreshFailed,
} from "./list-states.js";
import { describeManagerError } from "./request-errors.js";

type ManagersQuery = ReturnType<typeof useDownloadManagers>;

type DownloadManagersTableProps = Readonly<{
  query: ManagersQuery;
  // The id of the instance whose removal is in flight, if any.
  removing: string | undefined;
  removeError: unknown;
  onRemove: (id: string) => void;
}>;

const KIND_LABELS: Readonly<Record<DownloadManager["kind"], string>> = {
  radarr: "Radarr",
  sonarr: "Sonarr",
};

export function managerKindLabel(kind: DownloadManager["kind"]): string {
  return KIND_LABELS[kind];
}

// The route owns the query and decides what a 401 or 403 means; this renders
// every other state.
export function DownloadManagersTable({
  query,
  removing,
  removeError,
  onRemove,
}: DownloadManagersTableProps): ReactNode {
  if (query.status === "pending") {
    return <AsyncStatus>Loading download managers…</AsyncStatus>;
  }
  if (query.status === "error" && query.data === undefined) {
    return <LoadFailed noun="download managers" onRetry={query.refetch} />;
  }
  const items = query.data.pages.flatMap((page) => page.items);
  const refreshFailed = query.isError && !query.isFetchNextPageError;
  return (
    <>
      {refreshFailed ? (
        <RefreshFailed
          noun="download managers"
          retrying={query.isRefetching}
          onRetry={query.refetch}
        />
      ) : null}
      <ActionFailed summary={describeManagerError(removeError)} />
      {items.length === 0 ? (
        <p>No download manager is registered yet. Requests need one.</p>
      ) : (
        <Table managers={items} removing={removing} onRemove={onRemove} />
      )}
      <Pager
        noun="download managers"
        hasMore={query.hasNextPage}
        loading={query.isFetchingNextPage}
        failed={query.isFetchNextPageError}
        onMore={query.fetchNextPage}
      />
    </>
  );
}

function Table({
  managers,
  removing,
  onRemove,
}: Readonly<{
  managers: readonly DownloadManager[];
  removing: string | undefined;
  onRemove: (id: string) => void;
}>): ReactNode {
  return (
    <div
      className="table-scroll"
      role="region"
      aria-label="Download managers table"
      tabIndex={0}
    >
      <table>
        <caption>Download managers, ordered by name</caption>
        <thead>
          <tr>
            <th scope="col">Name</th>
            <th scope="col">Kind</th>
            <th scope="col">Address</th>
            <th scope="col">Transport</th>
            <th scope="col">Registered</th>
            <th scope="col">Actions</th>
          </tr>
        </thead>
        <tbody>
          {managers.map((manager) => (
            <tr key={manager.id}>
              <th scope="row">{manager.name}</th>
              <td>{managerKindLabel(manager.kind)}</td>
              <td>
                <code>{manager.base_url}</code>
              </td>
              <td>
                {manager.allow_insecure ? (
                  <span className="badge badge-warning">Plaintext HTTP</span>
                ) : (
                  <span className="badge badge-success">HTTPS</span>
                )}
              </td>
              <td>
                <time dateTime={manager.created_at}>
                  {manager.created_at.slice(0, 10)}
                </time>
              </td>
              <td>
                <ConfirmControls
                  action="Remove"
                  confirming="removing"
                  subject={manager.name}
                  busy={removing === manager.id}
                  busyLabel="Removing…"
                  onConfirm={() => {
                    onRemove(manager.id);
                  }}
                />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
