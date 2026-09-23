import { useEffect, useRef, useState, type ReactNode } from "react";
import { AsyncStatus } from "../../../components/async-status.js";
import type {
  Capabilities,
  MediaServer,
  MediaServerKind,
} from "../api/media-servers-schemas.js";
import type { useMediaServers } from "../hooks/media-servers-queries.js";
import { describeRemoveError } from "./media-server-errors.js";

type MediaServersQuery = ReturnType<typeof useMediaServers>;

type MediaServersTableProps = Readonly<{
  query: MediaServersQuery;
  // The id of the server whose removal is in flight, if any.
  removing: string | undefined;
  removeError: unknown;
  onRemove: (id: string) => void;
}>;

// The route owns the query and decides what a 401 or 403 means; this renders
// every other state: loading, empty, rows, a failed refresh that keeps the
// rows, a failed later page that keeps the rows, and a failed first load.
export function MediaServersTable({
  query,
  removing,
  removeError,
  onRemove,
}: MediaServersTableProps): ReactNode {
  if (query.status === "pending") {
    return <AsyncStatus>Loading media servers…</AsyncStatus>;
  }
  if (query.status === "error" && query.data === undefined) {
    return <LoadFailed onRetry={query.refetch} />;
  }
  const items = query.data.pages.flatMap((page) => page.items);
  const refreshFailed = query.isError && !query.isFetchNextPageError;
  const removeSummary = describeRemoveError(removeError);
  return (
    <>
      {refreshFailed ? (
        <RefreshFailed retrying={query.isRefetching} onRetry={query.refetch} />
      ) : null}
      {removeSummary === undefined ? null : (
        <AsyncStatus kind="alert">{removeSummary}</AsyncStatus>
      )}
      {items.length === 0 ? (
        <p>No media servers are registered yet.</p>
      ) : (
        <Table servers={items} removing={removing} onRemove={onRemove} />
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

type TableProps = Readonly<{
  servers: readonly MediaServer[];
  removing: string | undefined;
  onRemove: (id: string) => void;
}>;

function Table({ servers, removing, onRemove }: TableProps): ReactNode {
  return (
    <table className="media-servers-table">
      <caption>Media servers, ordered by name</caption>
      <thead>
        <tr>
          <th scope="col">Name</th>
          <th scope="col">Kind</th>
          <th scope="col">Address</th>
          <th scope="col">Capabilities</th>
          <th scope="col">Added</th>
          <th scope="col">Actions</th>
        </tr>
      </thead>
      <tbody>
        {servers.map((server) => (
          <ServerRow
            key={server.id}
            server={server}
            removing={removing === server.id}
            onRemove={onRemove}
          />
        ))}
      </tbody>
    </table>
  );
}

type ServerRowProps = Readonly<{
  server: MediaServer;
  removing: boolean;
  onRemove: (id: string) => void;
}>;

function ServerRow({ server, removing, onRemove }: ServerRowProps): ReactNode {
  return (
    <tr>
      <th scope="row">{server.name}</th>
      <td>{KIND_LABELS[server.kind]}</td>
      <td>
        <code>{server.base_url}</code>
        {server.allow_insecure ? (
          <p className="transport-warning">Plaintext HTTP allowed</p>
        ) : null}
      </td>
      <td>
        <CapabilityList
          serverName={server.name}
          capabilities={server.capabilities}
        />
      </td>
      <td>
        <time dateTime={server.created_at}>
          {server.created_at.slice(0, 10)}
        </time>
      </td>
      <td>
        <RemoveControls
          id={server.id}
          name={server.name}
          removing={removing}
          onRemove={onRemove}
        />
      </td>
    </tr>
  );
}

const KIND_LABELS: Readonly<Record<MediaServerKind, string>> = {
  jellyfin: "Jellyfin",
};

const CAPABILITY_LABELS: readonly (readonly [keyof Capabilities, string])[] = [
  ["create_user_with_password", "Create users"],
  ["set_password", "Set passwords"],
  ["quick_connect_approval", "Quick Connect"],
  ["provider_id_lookup", "Provider id lookup"],
];

function CapabilityList({
  serverName,
  capabilities,
}: Readonly<{ serverName: string; capabilities: Capabilities }>): ReactNode {
  const enabled = CAPABILITY_LABELS.filter(([key]) => capabilities[key]);
  if (enabled.length === 0) {
    return <span>None</span>;
  }
  return (
    <ul
      className="capability-list"
      aria-label={`Capabilities of ${serverName}`}
    >
      {enabled.map(([key, label]) => (
        <li key={key}>{label}</li>
      ))}
    </ul>
  );
}

type RemoveControlsProps = Readonly<{
  id: string;
  name: string;
  removing: boolean;
  onRemove: (id: string) => void;
}>;

// Removal is two clicks so a stray click cannot drop a server. The
// confirmation lives in the row, where the name being removed is visible;
// focus follows it there and comes back to Remove on cancel.
function RemoveControls({
  id,
  name,
  removing,
  onRemove,
}: RemoveControlsProps): ReactNode {
  const [confirming, setConfirming] = useState(false);
  const removeRef = useRef<HTMLButtonElement>(null);
  const confirmRef = useRef<HTMLButtonElement>(null);
  const returnFocus = useRef(false);
  useEffect(() => {
    if (confirming) {
      confirmRef.current?.focus();
    } else if (returnFocus.current) {
      returnFocus.current = false;
      removeRef.current?.focus();
    }
  }, [confirming]);
  if (removing) {
    return <span role="status">Removing…</span>;
  }
  if (!confirming) {
    return (
      <button
        ref={removeRef}
        type="button"
        onClick={() => {
          setConfirming(true);
        }}
      >
        Remove {name}
      </button>
    );
  }
  return (
    <span className="confirm-remove">
      <button
        ref={confirmRef}
        type="button"
        onClick={() => {
          setConfirming(false);
          onRemove(id);
        }}
      >
        Confirm removal of {name}
      </button>
      <button
        type="button"
        onClick={() => {
          returnFocus.current = true;
          setConfirming(false);
        }}
      >
        Cancel removal of {name}
      </button>
    </span>
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
      {failed ? (
        <p role="alert">More media servers could not be loaded.</p>
      ) : null}
      <button
        type="button"
        disabled={loading}
        onClick={() => {
          void onMore();
        }}
      >
        {loading ? "Loading more…" : "Load more media servers"}
      </button>
      <span role="status">{loading ? "Loading more media servers." : ""}</span>
    </div>
  );
}

type RetryProps = Readonly<{ onRetry: () => Promise<unknown> }>;

function LoadFailed({ onRetry }: RetryProps): ReactNode {
  return (
    <>
      <AsyncStatus kind="alert">
        The media servers could not be loaded.
      </AsyncStatus>
      <RetryButton retrying={false} onRetry={onRetry} />
    </>
  );
}

function RefreshFailed({
  retrying,
  onRetry,
}: RetryProps & Readonly<{ retrying: boolean }>): ReactNode {
  return (
    <div className="stale-warning">
      <AsyncStatus kind="alert">
        The media servers could not be refreshed. What is shown may be out of
        date.
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
