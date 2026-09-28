import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { AsyncStatus } from "../../../components/async-status.js";
import { LoadFailed, Pager } from "../../requests/components/list-states.js";
import type { AdminAccount } from "../api/accounts-schemas.js";
import type { useAccounts } from "../hooks/accounts-queries.js";
import {
  formatCreated,
  rolesLabel,
  signInMethodLabel,
} from "./account-format.js";

type AccountsTableProps = Readonly<{
  query: ReturnType<typeof useAccounts>;
  search: string;
}>;

// Every account the search matches, ordered by username, with its roles,
// how it signs in, and which media-server users it is linked to.
export function AccountsTable({
  query,
  search,
}: AccountsTableProps): ReactNode {
  if (query.status === "pending") {
    return <AsyncStatus>Loading accounts…</AsyncStatus>;
  }
  if (query.status === "error" && query.data === undefined) {
    return <LoadFailed noun="accounts" onRetry={query.refetch} />;
  }
  const accounts = query.data.pages.flatMap((page) => page.items);
  if (accounts.length === 0) {
    return (
      <p>
        {search === ""
          ? "No accounts exist yet."
          : `No account matches "${search}".`}
      </p>
    );
  }
  return (
    <>
      <div
        className="table-scroll"
        role="region"
        aria-label="Accounts table"
        tabIndex={0}
      >
        <table>
          <caption>
            {search === "" ? "Accounts" : `Accounts matching "${search}"`}
          </caption>
          <thead>
            <tr>
              <th scope="col">Username</th>
              <th scope="col">Roles</th>
              <th scope="col">Signs in with</th>
              <th scope="col">Media users</th>
              <th scope="col">Created</th>
            </tr>
          </thead>
          <tbody>
            {accounts.map((account) => (
              <AccountRow key={account.id} account={account} />
            ))}
          </tbody>
        </table>
      </div>
      <Pager
        noun="accounts"
        hasMore={query.hasNextPage}
        loading={query.isFetchingNextPage}
        failed={query.isFetchNextPageError}
        onMore={query.fetchNextPage}
      />
    </>
  );
}

function AccountRow({
  account,
}: Readonly<{ account: AdminAccount }>): ReactNode {
  return (
    <tr>
      <th scope="row">
        <Link to={`/admin/accounts/${encodeURIComponent(account.id)}`}>
          {account.username}
        </Link>{" "}
        {account.is_self ? (
          <span className="badge badge-neutral">You</span>
        ) : null}
      </th>
      <td>{rolesLabel(account.roles)}</td>
      <td>{signInMethodLabel(account.sign_in_method)}</td>
      <td>
        <MediaUsersSummary account={account} />
      </td>
      <td>
        <time dateTime={account.created_at}>
          {formatCreated(account.created_at)}
        </time>
      </td>
    </tr>
  );
}

// "Cabin: josh" per link; a suppressed link is one the operator hid.
export function MediaUsersSummary({
  account,
}: Readonly<{ account: AdminAccount }>): ReactNode {
  if (account.media_users.length === 0) {
    return "None";
  }
  return (
    <ul className="plain-list">
      {account.media_users.map((link) => (
        <li key={`${link.media_server_id}:${link.media_user_id}`}>
          {link.media_server_name}: {link.username}
          {link.suppressed ? " (suppressed)" : ""}
        </li>
      ))}
    </ul>
  );
}
