import type { ReactNode } from "react";
import { AsyncStatus } from "../../../components/async-status.js";
import { ConfirmControls } from "../../requests/components/confirm-controls.js";
import {
  ActionFailed,
  LoadFailed,
  Pager,
  RefreshFailed,
} from "../../requests/components/list-states.js";
import type { NotificationChannel } from "../api/notification-schemas.js";
import type { useChannels } from "../hooks/notifications-queries.js";
import { describeChannelError } from "./notification-errors.js";
import { eventLabel, kindLabel } from "./notification-format.js";

type ChannelsTableProps = Readonly<{
  query: ReturnType<typeof useChannels>;
  // The id of the channel whose removal or test is in flight, if any.
  removing: string | undefined;
  testing: string | undefined;
  actionError: unknown;
  // Which channel's deliveries are shown, if any.
  shown: string | undefined;
  onEdit: (channel: NotificationChannel) => void;
  onTest: (id: string) => void;
  onShowDeliveries: (id: string | undefined) => void;
  onRemove: (id: string) => void;
}>;

export function ChannelsTable({
  query,
  removing,
  testing,
  actionError,
  shown,
  onEdit,
  onTest,
  onShowDeliveries,
  onRemove,
}: ChannelsTableProps): ReactNode {
  if (query.status === "pending") {
    return <AsyncStatus>Loading channels…</AsyncStatus>;
  }
  if (query.status === "error" && query.data === undefined) {
    return <LoadFailed noun="channels" onRetry={query.refetch} />;
  }
  const items = query.data.pages.flatMap((page) => page.items);
  const refreshFailed = query.isError && !query.isFetchNextPageError;
  return (
    <>
      {refreshFailed ? (
        <RefreshFailed
          noun="channels"
          retrying={query.isRefetching}
          onRetry={query.refetch}
        />
      ) : null}
      <ActionFailed summary={describeChannelError(actionError)} />
      {items.length === 0 ? (
        <p>No channel is registered yet. Nothing is delivered until one is.</p>
      ) : (
        <div
          className="table-scroll"
          role="region"
          aria-label="Notification channels table"
          tabIndex={0}
        >
          <table>
            <caption>Notification channels, ordered by name</caption>
            <thead>
              <tr>
                <th scope="col">Name</th>
                <th scope="col">Kind</th>
                <th scope="col">State</th>
                <th scope="col">Events</th>
                <th scope="col">Actions</th>
              </tr>
            </thead>
            <tbody>
              {items.map((channel) => (
                <ChannelRow
                  key={channel.id}
                  channel={channel}
                  removing={removing === channel.id}
                  testing={testing === channel.id}
                  shown={shown === channel.id}
                  onEdit={onEdit}
                  onTest={onTest}
                  onShowDeliveries={onShowDeliveries}
                  onRemove={onRemove}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}
      <Pager
        noun="channels"
        hasMore={query.hasNextPage}
        loading={query.isFetchingNextPage}
        failed={query.isFetchNextPageError}
        onMore={query.fetchNextPage}
      />
    </>
  );
}

function ChannelRow({
  channel,
  removing,
  testing,
  shown,
  onEdit,
  onTest,
  onShowDeliveries,
  onRemove,
}: Readonly<{
  channel: NotificationChannel;
  removing: boolean;
  testing: boolean;
  shown: boolean;
  onEdit: (channel: NotificationChannel) => void;
  onTest: (id: string) => void;
  onShowDeliveries: (id: string | undefined) => void;
  onRemove: (id: string) => void;
}>): ReactNode {
  return (
    <tr>
      <th scope="row">{channel.name}</th>
      <td>{kindLabel(channel.kind)}</td>
      <td>
        <StateBadges channel={channel} />
      </td>
      <td>{channel.subscriptions.map(eventLabel).join(", ")}</td>
      <td>
        <span className="row-actions">
          <button
            type="button"
            onClick={() => {
              onEdit(channel);
            }}
          >
            Edit <span className="visually-hidden">{channel.name}</span>
          </button>
          {testing ? (
            <span role="status">Sending a test…</span>
          ) : (
            <button
              type="button"
              onClick={() => {
                onTest(channel.id);
              }}
            >
              Test <span className="visually-hidden">{channel.name}</span>
            </button>
          )}
          <button
            type="button"
            aria-pressed={shown}
            onClick={() => {
              onShowDeliveries(shown ? undefined : channel.id);
            }}
          >
            Deliveries{" "}
            <span className="visually-hidden">of {channel.name}</span>
          </button>
          <ConfirmControls
            action="Remove"
            confirming="removing"
            subject={channel.name}
            busy={removing}
            busyLabel="Removing…"
            onConfirm={() => {
              onRemove(channel.id);
            }}
          />
        </span>
      </td>
    </tr>
  );
}

function StateBadges({
  channel,
}: Readonly<{ channel: NotificationChannel }>): ReactNode {
  return (
    <span className="row-actions">
      {channel.enabled ? (
        <span className="badge badge-success">Enabled</span>
      ) : (
        <span className="badge badge-neutral">Disabled</span>
      )}
      {channel.consecutive_failures >= 3 ? (
        <span className="badge badge-danger">Degraded</span>
      ) : channel.consecutive_failures > 0 ? (
        <span className="badge badge-warning">
          {String(channel.consecutive_failures)} failing
        </span>
      ) : null}
    </span>
  );
}
