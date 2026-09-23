import type { ReactNode } from "react";
import { AsyncStatus } from "../../../components/async-status.js";
import type { Role } from "../api/roles-schemas.js";
import type { useRoles } from "../hooks/roles-queries.js";
import { RoleQuotaControls } from "./role-quota-controls.js";

type RolesQuery = ReturnType<typeof useRoles>;

// The route owns the query and decides what a 401 or 403 means; this renders
// every other state: loading, empty, rows, a failed refresh that keeps the
// rows, a failed later page that keeps the rows, and a failed first load.
export function RolesTable({
  query,
  accountId,
}: Readonly<{ query: RolesQuery; accountId: string }>): ReactNode {
  if (query.status === "pending") {
    return <AsyncStatus>Loading roles…</AsyncStatus>;
  }
  if (query.status === "error" && query.data === undefined) {
    return <LoadFailed onRetry={query.refetch} />;
  }
  const items = query.data.pages.flatMap((page) => page.items);
  const refreshFailed = query.isError && !query.isFetchNextPageError;
  return (
    <>
      {refreshFailed ? (
        <RefreshFailed retrying={query.isRefetching} onRetry={query.refetch} />
      ) : null}
      {items.length === 0 ? (
        <p>There are no roles yet.</p>
      ) : (
        <Table roles={items} accountId={accountId} />
      )}
      <Pager
        hasMore={query.hasNextPage}
        loading={query.isFetchingNextPage}
        failed={query.isFetchNextPageError}
        onMore={query.fetchNextPage}
      />
    </>
  );
}

function Table({
  roles,
  accountId,
}: Readonly<{ roles: readonly Role[]; accountId: string }>): ReactNode {
  return (
    <div
      className="table-scroll"
      role="region"
      aria-label="Roles table"
      tabIndex={0}
    >
      <table className="roles-table">
        <caption>Roles, ordered by name</caption>
        <thead>
          <tr>
            <th scope="col">Name</th>
            <th scope="col">Description</th>
            <th scope="col">Kind</th>
            <th scope="col">Permissions</th>
            <th scope="col">Request quota</th>
          </tr>
        </thead>
        <tbody>
          {roles.map((role) => (
            <RoleRow key={role.id} role={role} accountId={accountId} />
          ))}
        </tbody>
      </table>
    </div>
  );
}

function RoleRow({
  role,
  accountId,
}: Readonly<{ role: Role; accountId: string }>): ReactNode {
  return (
    <tr>
      <th scope="row">{role.name}</th>
      <td>{role.description}</td>
      <td>{role.built_in ? "Built-in" : "Custom"}</td>
      <td>
        <PermissionList roleName={role.name} permissions={role.permissions} />
      </td>
      <td>
        <RoleQuotaControls
          accountId={accountId}
          roleId={role.id}
          roleName={role.name}
        />
      </td>
    </tr>
  );
}

type PermissionListProps = Readonly<{
  roleName: string;
  permissions: readonly string[];
}>;

function PermissionList({
  roleName,
  permissions,
}: PermissionListProps): ReactNode {
  if (permissions.length === 0) {
    return <span>None</span>;
  }
  return (
    <ul className="permission-list" aria-label={`Permissions of ${roleName}`}>
      {permissions.map((permission) => (
        <li key={permission}>
          <code>{permission}</code>
        </li>
      ))}
    </ul>
  );
}

type PagerProps = Readonly<{
  hasMore: boolean;
  loading: boolean;
  failed: boolean;
  onMore: () => Promise<unknown>;
}>;

function Pager({ hasMore, loading, failed, onMore }: PagerProps): ReactNode {
  if (!hasMore) {
    return null;
  }
  return (
    <div className="pager">
      {failed ? <p role="alert">More roles could not be loaded.</p> : null}
      <button
        type="button"
        disabled={loading}
        onClick={() => {
          void onMore();
        }}
      >
        {loading ? "Loading more…" : "Load more roles"}
      </button>
      <span role="status">{loading ? "Loading more roles." : ""}</span>
    </div>
  );
}

type RetryProps = Readonly<{ onRetry: () => Promise<unknown> }>;

function LoadFailed({ onRetry }: RetryProps): ReactNode {
  return (
    <>
      <AsyncStatus kind="alert">The roles could not be loaded.</AsyncStatus>
      <RetryButton retrying={false} onRetry={onRetry} />
    </>
  );
}

// A background refresh failed; the rows below are the last known answer.
function RefreshFailed({
  retrying,
  onRetry,
}: RetryProps & Readonly<{ retrying: boolean }>): ReactNode {
  return (
    <div className="stale-warning">
      <AsyncStatus kind="alert">
        The roles could not be refreshed. What is shown may be out of date.
      </AsyncStatus>
      <RetryButton retrying={retrying} onRetry={onRetry} />
    </div>
  );
}

function RetryButton({
  retrying,
  onRetry,
}: RetryProps & Readonly<{ retrying: boolean }>): ReactNode {
  return (
    <button
      type="button"
      disabled={retrying}
      onClick={() => {
        void onRetry();
      }}
    >
      {retrying ? "Retrying…" : "Retry"}
    </button>
  );
}
