import { useEffect, useId, useMemo, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import {
  useSession,
  useSessionRecheck,
} from "../features/auth/hooks/auth-queries.js";
import { hasAnyPermission, permissions } from "../features/auth/permissions.js";
import {
  requestStatusSchema,
  type RequestStatus,
} from "../features/requests/api/requests-schemas.js";
import { SignInNotConfirmed } from "../features/requests/components/list-states.js";
import { statusLabel } from "../features/requests/components/request-format.js";
import { RequestsTable } from "../features/requests/components/requests-table.js";
import {
  useDecideRequest,
  useRequestProfiles,
  useRequests,
} from "../features/requests/hooks/requests-queries.js";
import { accessDenial } from "../lib/api/errors.js";
import { AccessDeniedRoute } from "./access-denied-route.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

export default function RequestsAdminRoute(): ReactNode {
  usePageTitle(pageTitle("Requests"));
  const session = useSession();
  const accountId = session.data?.account.id;
  const canReadProfiles =
    session.data !== undefined &&
    session.data !== null &&
    hasAnyPermission(session.data.permissions, [
      permissions.adminSettings,
      permissions.requestsCreate,
    ]);
  // The route guard only renders this page for a signed-in account. Keying
  // the page on the account remounts it when the principal changes, so a
  // late answer to one account's action can never show under another's.
  if (accountId === undefined) {
    return null;
  }
  return (
    <RequestsPage
      key={accountId}
      accountId={accountId}
      canReadProfiles={canReadProfiles}
    />
  );
}

type StatusChoice = RequestStatus | "all";

function RequestsPage({
  accountId,
  canReadProfiles,
}: Readonly<{ accountId: string; canReadProfiles: boolean }>): ReactNode {
  const [status, setStatus] = useState<StatusChoice>("pending");
  const requests = useRequests(accountId, status === "all" ? {} : { status });
  const decide = useDecideRequest(accountId);
  const profileNames = useProfileNames(accountId, canReadProfiles);
  const denial = accessDenial(requests.error) ?? accessDenial(decide.error);
  // The server denied something the cached session says is allowed: the
  // session is gone, or a permission was taken away. Re-reading the session
  // lets the route guard send the person to sign in or off this page.
  useSessionRecheck(
    denial !== undefined,
    Math.max(requests.errorUpdatedAt, decide.submittedAt),
  );
  if (denial === "forbidden") {
    return <AccessDeniedRoute />;
  }
  return (
    <>
      <h1>Requests</h1>
      <p className="page-intro">
        Pending requests wait here for a decision. An approved request is sent
        to the download manager named by its profile; a declined one is closed
        with the reason you give.
        {canReadProfiles ? (
          <>
            {" "}
            <Link to="/admin/request-profiles">Manage request settings.</Link>
          </>
        ) : null}
      </p>
      <section aria-labelledby="requests-heading" className="card">
        <h2 id="requests-heading">Requests</h2>
        <StatusFilter value={status} onChange={setStatus} />
        {denial === "unauthenticated" ? (
          <SignInNotConfirmed noun="requests" onRetry={requests.refetch} />
        ) : (
          <RequestsTable
            query={requests}
            accountId={accountId}
            profileNames={profileNames}
            deciding={decide.isPending ? decide.variables.id : undefined}
            decideError={decide.error}
            onDecide={(decision) => {
              decide.mutate(decision);
            }}
          />
        )}
      </section>
    </>
  );
}

const MAX_PROFILE_PAGES = 20;

// Profile names for the table. Listing profiles needs admin.settings; an
// approver without it sees the profile id's absence as "Unknown profile".
function useProfileNames(
  accountId: string,
  enabled: boolean,
): ReadonlyMap<string, string> {
  const profiles = useRequestProfiles(accountId, enabled);
  const {
    hasNextPage,
    isFetchingNextPage,
    isFetchNextPageError,
    fetchNextPage,
  } = profiles;
  const pageCount = profiles.data?.pages.length ?? 0;
  useEffect(() => {
    if (
      enabled &&
      hasNextPage &&
      !isFetchingNextPage &&
      !isFetchNextPageError &&
      pageCount < MAX_PROFILE_PAGES
    ) {
      void fetchNextPage();
    }
  }, [
    enabled,
    hasNextPage,
    isFetchingNextPage,
    isFetchNextPageError,
    pageCount,
    fetchNextPage,
  ]);
  return useMemo(
    () =>
      new Map(
        (profiles.data?.pages ?? []).flatMap((page) =>
          page.items.map((profile) => [profile.id, profile.name] as const),
        ),
      ),
    [profiles.data],
  );
}

function StatusFilter({
  value,
  onChange,
}: Readonly<{
  value: StatusChoice;
  onChange: (value: StatusChoice) => void;
}>): ReactNode {
  const id = useId();
  return (
    <div className="filter">
      <label htmlFor={id}>Status</label>
      <select
        id={id}
        value={value}
        onChange={(event) => {
          const next = event.target.value;
          onChange(next === "all" ? "all" : requestStatusSchema.parse(next));
        }}
      >
        <option value="all">All statuses</option>
        {requestStatusSchema.options.map((option) => (
          <option key={option} value={option}>
            {statusLabel(option)}
          </option>
        ))}
      </select>
    </div>
  );
}
