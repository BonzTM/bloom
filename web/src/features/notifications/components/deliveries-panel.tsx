import type { ReactNode } from "react";
import { AsyncStatus } from "../../../components/async-status.js";
import {
  LoadFailed,
  Pager,
  RefreshFailed,
} from "../../requests/components/list-states.js";
import { useDeliveries } from "../hooks/notifications-queries.js";
import { deliveryBadgeClass, eventLabel } from "./notification-format.js";

type DeliveriesPanelProps = Readonly<{
  accountId: string;
  channelId: string;
  channelName: string;
}>;

// Recent deliveries for one channel: what was sent, what is waiting for a
// retry, and what gave up, with the safe reason the worker kept.
export function DeliveriesPanel({
  accountId,
  channelId,
  channelName,
}: DeliveriesPanelProps): ReactNode {
  const query = useDeliveries(accountId, channelId);
  if (query.status === "pending") {
    return <AsyncStatus>Loading deliveries…</AsyncStatus>;
  }
  if (query.status === "error" && query.data === undefined) {
    return <LoadFailed noun="deliveries" onRetry={query.refetch} />;
  }
  const items = query.data.pages.flatMap((page) => page.items);
  const refreshFailed = query.isError && !query.isFetchNextPageError;
  return (
    <>
      {refreshFailed ? (
        <RefreshFailed
          noun="deliveries"
          retrying={query.isRefetching}
          onRetry={query.refetch}
        />
      ) : null}
      {items.length === 0 ? (
        <p>Nothing has been delivered through {channelName} yet.</p>
      ) : (
        <div
          className="table-scroll"
          role="region"
          aria-label={`Deliveries of ${channelName} table`}
          tabIndex={0}
        >
          <table>
            <caption>Deliveries of {channelName}, newest first</caption>
            <thead>
              <tr>
                <th scope="col">Event</th>
                <th scope="col">Request</th>
                <th scope="col">Status</th>
                <th scope="col">Attempts</th>
                <th scope="col">When</th>
                <th scope="col">Last error</th>
              </tr>
            </thead>
            <tbody>
              {items.map((delivery) => (
                <tr key={delivery.id}>
                  <th scope="row">{eventLabel(delivery.event_type)}</th>
                  <td>
                    <code title={delivery.request_id}>
                      {delivery.request_id.slice(0, 8)}
                    </code>
                  </td>
                  <td>
                    <span className={deliveryBadgeClass(delivery.status)}>
                      {delivery.status}
                    </span>
                  </td>
                  <td>{String(delivery.attempts)}</td>
                  <td>
                    <time
                      dateTime={delivery.sent_at ?? delivery.next_attempt_at}
                    >
                      {(delivery.sent_at ?? delivery.next_attempt_at)
                        .slice(0, 16)
                        .replace("T", " ")}
                    </time>
                  </td>
                  <td>
                    {delivery.last_error === "" ? "—" : delivery.last_error}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <Pager
        noun="deliveries"
        hasMore={query.hasNextPage}
        loading={query.isFetchingNextPage}
        failed={query.isFetchNextPageError}
        onMore={query.fetchNextPage}
      />
    </>
  );
}
