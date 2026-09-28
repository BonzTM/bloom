import type { ReactNode } from "react";
import { Link, useParams } from "react-router-dom";
import { AsyncStatus } from "../components/async-status.js";
import { accountIdSchema } from "../features/accounts/api/accounts-schemas.js";
import {
  formatCreated,
  rolesLabel,
  signInMethodLabel,
} from "../features/accounts/components/account-format.js";
import { MediaUsersSummary } from "../features/accounts/components/accounts-table.js";
import { useAccount } from "../features/accounts/hooks/accounts-queries.js";
import {
  useSession,
  useSessionRecheck,
} from "../features/auth/hooks/auth-queries.js";
import { ReportFailed } from "../features/playback/components/stats-panels.js";
import { accessDenial, ApiError } from "../lib/api/errors.js";
import { AccessDeniedRoute } from "./access-denied-route.js";
import { NotFoundRoute } from "./not-found-route.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

// One account: the same facts as the list, laid out for reading.
export default function AccountRoute(): ReactNode {
  const { accountId } = useParams();
  const session = useSession();
  const viewerId = session.data?.account.id;
  const parsed = accountIdSchema.safeParse(accountId);
  if (!parsed.success) {
    return <NotFoundRoute />;
  }
  if (viewerId === undefined) {
    return null;
  }
  return (
    <AccountPage
      key={`${viewerId}:${parsed.data}`}
      viewerId={viewerId}
      accountId={parsed.data}
    />
  );
}

function AccountPage({
  viewerId,
  accountId,
}: Readonly<{ viewerId: string; accountId: string }>): ReactNode {
  const account = useAccount(viewerId, accountId);
  usePageTitle(pageTitle(account.data?.username ?? "Account"));
  const denial = accessDenial(account.error);
  useSessionRecheck(denial !== undefined, account.errorUpdatedAt);
  if (denial === "forbidden") {
    return <AccessDeniedRoute />;
  }
  if (account.error instanceof ApiError && account.error.status === 404) {
    return <NotFoundRoute />;
  }
  if (account.status === "pending") {
    return <AsyncStatus>Loading account…</AsyncStatus>;
  }
  if (account.status === "error") {
    return <ReportFailed what="The account" onRetry={account.refetch} />;
  }
  const item = account.data;
  return (
    <>
      <p>
        <Link to="/admin/accounts">All accounts</Link>
      </p>
      <h1>
        {item.username}{" "}
        {item.is_self ? <span className="badge badge-neutral">You</span> : null}
      </h1>
      <dl className="facts">
        <div>
          <dt>Roles</dt>
          <dd>{rolesLabel(item.roles)}</dd>
        </div>
        <div>
          <dt>Signs in with</dt>
          <dd>{signInMethodLabel(item.sign_in_method)}</dd>
        </div>
        <div>
          <dt>Media users</dt>
          <dd>
            <MediaUsersSummary account={item} />
          </dd>
        </div>
        <div>
          <dt>Created</dt>
          <dd>
            <time dateTime={item.created_at}>
              {formatCreated(item.created_at)}
            </time>
          </dd>
        </div>
      </dl>
    </>
  );
}
