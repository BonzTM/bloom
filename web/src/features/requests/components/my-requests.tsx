import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { AsyncStatus } from "../../../components/async-status.js";
import type { MediaRequest } from "../api/requests-schemas.js";
import type { useRequests } from "../hooks/requests-queries.js";
import { LoadFailed, Pager, RefreshFailed } from "./list-states.js";
import { Poster } from "./poster.js";
import {
  seasonsLabel,
  statusBadgeClass,
  statusLabel,
  titleWithYear,
} from "./request-format.js";
import { titlePath } from "./title-grid.js";

type MyRequestsProps = Readonly<{
  query: ReturnType<typeof useRequests>;
}>;

// The signed-in account's own requests, newest first, as a list of cards.
export function MyRequests({ query }: MyRequestsProps): ReactNode {
  if (query.status === "pending") {
    return <AsyncStatus>Loading your requests…</AsyncStatus>;
  }
  if (query.status === "error" && query.data === undefined) {
    return <LoadFailed noun="requests" onRetry={query.refetch} />;
  }
  const items = query.data.pages.flatMap((page) => page.items);
  const refreshFailed = query.isError && !query.isFetchNextPageError;
  return (
    <>
      {refreshFailed ? (
        <RefreshFailed
          noun="requests"
          retrying={query.isRefetching}
          onRetry={query.refetch}
        />
      ) : null}
      {items.length === 0 ? (
        <p>You have not requested anything yet.</p>
      ) : (
        <ul className="request-list" aria-label="Your requests, newest first">
          {items.map((request) => (
            <RequestCard key={request.id} request={request} />
          ))}
        </ul>
      )}
      <Pager
        noun="requests"
        hasMore={query.hasNextPage}
        loading={query.isFetchingNextPage}
        failed={query.isFetchNextPageError}
        onMore={query.fetchNextPage}
      />
    </>
  );
}

function RequestCard({
  request,
}: Readonly<{ request: MediaRequest }>): ReactNode {
  const seasons = seasonsLabel(request);
  return (
    <li className="request-card">
      <Link to={titlePath(request)} className="request-card-poster">
        <Poster
          posterPath={request.poster_path}
          title={request.title}
          size="w342"
        />
      </Link>
      <div className="request-card-body">
        <h3>
          <Link to={titlePath(request)}>{titleWithYear(request)}</Link>
        </h3>
        {seasons === "" ? null : <p className="row-detail">{seasons}</p>}
        <p>
          <span className={statusBadgeClass(request.status)}>
            {statusLabel(request.status)}
          </span>{" "}
          <time dateTime={request.created_at}>
            {request.created_at.slice(0, 10)}
          </time>
        </p>
        {request.decision_reason === "" ? null : (
          <p className="row-detail">{request.decision_reason}</p>
        )}
      </div>
    </li>
  );
}
