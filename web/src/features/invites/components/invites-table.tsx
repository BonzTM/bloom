import { useEffect, useRef, useState, type ReactNode } from "react";
import { AsyncStatus } from "../../../components/async-status.js";
import type { Invite, InviteStatus } from "../api/invites-schemas.js";
import type { useInvites } from "../hooks/invites-queries.js";
import { describeRevokeInviteError } from "./invite-errors.js";

type InvitesQuery = ReturnType<typeof useInvites>;

type InvitesTableProps = Readonly<{
  query: InvitesQuery;
  serverNames: ReadonlyMap<string, string>;
  // The id of the invite whose revocation is in flight, if any.
  revoking: string | undefined;
  revokeError: unknown;
  onRevoke: (id: string) => void;
}>;

// The route owns the query and decides what a 401 or 403 means; this renders
// every other state: loading, empty, rows, a failed refresh that keeps the
// rows, a failed later page that keeps the rows, and a failed first load.
export function InvitesTable({
  query,
  serverNames,
  revoking,
  revokeError,
  onRevoke,
}: InvitesTableProps): ReactNode {
  if (query.status === "pending") {
    return <AsyncStatus>Loading invites…</AsyncStatus>;
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
      <RevokeFailed error={revokeError} />
      {items.length === 0 ? (
        <p>No invites have been created yet.</p>
      ) : (
        <Table
          invites={items}
          serverNames={serverNames}
          revoking={revoking}
          onRevoke={onRevoke}
        />
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

// A failed revocation replaced the focused control with nothing, so the
// explanation takes focus itself.
function RevokeFailed({ error }: Readonly<{ error: unknown }>): ReactNode {
  const ref = useRef<HTMLParagraphElement>(null);
  const summary = describeRevokeInviteError(error);
  useEffect(() => {
    if (summary !== undefined) {
      ref.current?.focus();
    }
  }, [summary]);
  if (summary === undefined) {
    return null;
  }
  return (
    <p role="alert" tabIndex={-1} ref={ref}>
      {summary}
    </p>
  );
}

type TableProps = Readonly<{
  invites: readonly Invite[];
  serverNames: ReadonlyMap<string, string>;
  revoking: string | undefined;
  onRevoke: (id: string) => void;
}>;

function Table({
  invites,
  serverNames,
  revoking,
  onRevoke,
}: TableProps): ReactNode {
  return (
    <table className="invites-table">
      <caption>Invites, newest first</caption>
      <thead>
        <tr>
          <th scope="col">Label</th>
          <th scope="col">Server</th>
          <th scope="col">Status</th>
          <th scope="col">Uses</th>
          <th scope="col">Expires</th>
          <th scope="col">Created</th>
          <th scope="col">Actions</th>
        </tr>
      </thead>
      <tbody>
        {invites.map((invite) => (
          <InviteRow
            key={invite.id}
            invite={invite}
            serverName={serverNames.get(invite.media_server_id)}
            revoking={revoking === invite.id}
            onRevoke={onRevoke}
          />
        ))}
      </tbody>
    </table>
  );
}

type InviteRowProps = Readonly<{
  invite: Invite;
  serverName: string | undefined;
  revoking: boolean;
  onRevoke: (id: string) => void;
}>;

function InviteRow({
  invite,
  serverName,
  revoking,
  onRevoke,
}: InviteRowProps): ReactNode {
  return (
    <tr>
      <th scope="row">{invite.label}</th>
      <td>{serverName ?? "Unknown server"}</td>
      <td>{STATUS_LABELS[invite.status]}</td>
      <td>{usesLabel(invite)}</td>
      <td>
        {invite.expires_at === undefined ? (
          "Never"
        ) : (
          <time dateTime={invite.expires_at}>
            {invite.expires_at.slice(0, 10)}
          </time>
        )}
      </td>
      <td>
        <time dateTime={invite.created_at}>
          {invite.created_at.slice(0, 10)}
        </time>
      </td>
      <td>
        {invite.status === "active" ? (
          <RevokeControls
            id={invite.id}
            label={invite.label}
            revoking={revoking}
            onRevoke={onRevoke}
          />
        ) : (
          <span aria-hidden="true">—</span>
        )}
      </td>
    </tr>
  );
}

const STATUS_LABELS: Readonly<Record<InviteStatus, string>> = {
  active: "Active",
  expired: "Expired",
  exhausted: "Used up",
  revoked: "Revoked",
};

function usesLabel(invite: Invite): string {
  const used = String(invite.use_count);
  return invite.max_uses === undefined
    ? `${used}, no limit`
    : `${used} of ${String(invite.max_uses)}`;
}

type RevokeControlsProps = Readonly<{
  id: string;
  label: string;
  revoking: boolean;
  onRevoke: (id: string) => void;
}>;

// Revocation is two clicks so a stray click cannot kill a link that is
// already in someone's inbox. Focus follows the confirmation and comes back
// to Revoke on cancel.
function RevokeControls({
  id,
  label,
  revoking,
  onRevoke,
}: RevokeControlsProps): ReactNode {
  const [confirming, setConfirming] = useState(false);
  const revokeRef = useRef<HTMLButtonElement>(null);
  const confirmRef = useRef<HTMLButtonElement>(null);
  const returnFocus = useRef(false);
  useEffect(() => {
    if (confirming) {
      confirmRef.current?.focus();
    } else if (returnFocus.current) {
      returnFocus.current = false;
      revokeRef.current?.focus();
    }
  }, [confirming]);
  if (revoking) {
    return <span role="status">Revoking…</span>;
  }
  if (!confirming) {
    return (
      <button
        ref={revokeRef}
        type="button"
        onClick={() => {
          setConfirming(true);
        }}
      >
        Revoke {label}
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
          onRevoke(id);
        }}
      >
        Confirm revoking {label}
      </button>
      <button
        type="button"
        onClick={() => {
          returnFocus.current = true;
          setConfirming(false);
        }}
      >
        Cancel revoking {label}
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
      {failed ? <p role="alert">More invites could not be loaded.</p> : null}
      <button
        type="button"
        disabled={loading}
        onClick={() => {
          void onMore();
        }}
      >
        {loading ? "Loading more…" : "Load more invites"}
      </button>
      <span role="status">{loading ? "Loading more invites." : ""}</span>
    </div>
  );
}

type RetryProps = Readonly<{ onRetry: () => Promise<unknown> }>;

function LoadFailed({ onRetry }: RetryProps): ReactNode {
  return (
    <>
      <AsyncStatus kind="alert">The invites could not be loaded.</AsyncStatus>
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
        The invites could not be refreshed. What is shown may be out of date.
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
