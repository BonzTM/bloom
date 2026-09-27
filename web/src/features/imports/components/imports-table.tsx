import type { ReactNode } from "react";
import { AsyncStatus } from "../../../components/async-status.js";
import { formatCount } from "../../../components/charts/chart-data.js";
import {
  ActionFailed,
  LoadFailed,
  Pager,
  RefreshFailed,
} from "../../requests/components/list-states.js";
import type { ImportJob } from "../api/imports-schemas.js";
import { isActive, type useImports } from "../hooks/imports-queries.js";
import { describeImportError } from "./import-errors.js";
import {
  formatStamp,
  sourceLabel,
  stateBadgeClass,
  stateLabel,
} from "./import-format.js";

type ImportsTableProps = Readonly<{
  query: ReturnType<typeof useImports>;
  serverName: (id: string) => string;
  // The id of the job whose cancellation is in flight, if any.
  cancelling: string | undefined;
  actionError: unknown;
  onCancel: (id: string) => void;
}>;

export function ImportsTable({
  query,
  serverName,
  cancelling,
  actionError,
  onCancel,
}: ImportsTableProps): ReactNode {
  if (query.status === "pending") {
    return <AsyncStatus>Loading imports…</AsyncStatus>;
  }
  if (query.status === "error" && query.data === undefined) {
    return <LoadFailed noun="imports" onRetry={query.refetch} />;
  }
  const items = query.data.pages.flatMap((page) => page.items);
  const refreshFailed = query.isError && !query.isFetchNextPageError;
  return (
    <>
      {refreshFailed ? (
        <RefreshFailed
          noun="imports"
          retrying={query.isRefetching}
          onRetry={query.refetch}
        />
      ) : null}
      <ActionFailed summary={describeImportError(actionError)} />
      {items.length === 0 ? (
        <p>No import has been started yet.</p>
      ) : (
        <div
          className="table-scroll"
          role="region"
          aria-label="Imports table"
          tabIndex={0}
        >
          <table>
            <caption>Imports, newest first</caption>
            <thead>
              <tr>
                <th scope="col">Source</th>
                <th scope="col">Server</th>
                <th scope="col">State</th>
                <th scope="col">Read</th>
                <th scope="col">Imported</th>
                <th scope="col">Skipped</th>
                <th scope="col">Duplicates</th>
                <th scope="col">Started</th>
                <th scope="col">Finished</th>
                <th scope="col">Actions</th>
              </tr>
            </thead>
            <tbody>
              {items.map((job) => (
                <JobRow
                  key={job.id}
                  job={job}
                  server={serverName(job.media_server_id)}
                  cancelling={cancelling === job.id}
                  onCancel={onCancel}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}
      <Pager
        noun="imports"
        hasMore={query.hasNextPage}
        loading={query.isFetchingNextPage}
        failed={query.isFetchNextPageError}
        onMore={query.fetchNextPage}
      />
    </>
  );
}

function JobRow({
  job,
  server,
  cancelling,
  onCancel,
}: Readonly<{
  job: ImportJob;
  server: string;
  cancelling: boolean;
  onCancel: (id: string) => void;
}>): ReactNode {
  const label = `${sourceLabel(job.source)} from ${server}`;
  return (
    <tr>
      <th scope="row">{sourceLabel(job.source)}</th>
      <td>{server}</td>
      <td>
        <span className={stateBadgeClass(job.state)}>
          {stateLabel(job.state)}
        </span>
        {job.last_error === "" ? null : (
          <span className="row-detail"> {job.last_error}</span>
        )}
      </td>
      <td>{formatCount(job.read)}</td>
      <td>{formatCount(job.imported)}</td>
      <td>{formatCount(job.skipped)}</td>
      <td>{formatCount(job.duplicate)}</td>
      <td>
        <time dateTime={job.started_at ?? job.created_at}>
          {formatStamp(job.started_at ?? job.created_at)}
        </time>
      </td>
      <td>
        {job.finished_at === undefined ? null : (
          <time dateTime={job.finished_at}>{formatStamp(job.finished_at)}</time>
        )}
      </td>
      <td>
        {isActive(job) ? (
          <button
            type="button"
            disabled={cancelling}
            onClick={() => {
              onCancel(job.id);
            }}
          >
            {cancelling ? "Cancelling…" : "Cancel"}
            <span className="visually-hidden"> {label}</span>
          </button>
        ) : null}
      </td>
    </tr>
  );
}
