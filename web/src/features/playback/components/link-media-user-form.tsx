import { useId, useState, type ReactNode } from "react";
import { AsyncStatus } from "../../../components/async-status.js";
import { ApiError } from "../../../lib/api/errors.js";
import {
  useLinkMediaUser,
  useMediaServerUsers,
} from "../hooks/stats-queries.js";

type LinkMediaUserFormProps = Readonly<{
  accountId: string;
  servers: readonly { id: string; name: string }[];
}>;

// An administrator links their own account to a media-server user: pick the
// server, pick the user, link. The page re-reads "me" on success.
export function LinkMediaUserForm({
  accountId,
  servers,
}: LinkMediaUserFormProps): ReactNode {
  const id = useId();
  const [serverId, setServerId] = useState<string | undefined>(
    servers.length === 1 ? servers[0]?.id : undefined,
  );
  const [userId, setUserId] = useState("");
  const users = useMediaServerUsers(accountId, serverId);
  const link = useLinkMediaUser(accountId);
  const summary = describeLinkError(link.error);
  return (
    <form
      aria-label="Link your account to a media-server user"
      className="stack"
      onSubmit={(event) => {
        event.preventDefault();
        if (serverId === undefined || userId === "" || link.isPending) {
          return;
        }
        link.mutate({ mediaServerId: serverId, mediaUserId: userId });
      }}
    >
      {summary === undefined ? null : (
        <AsyncStatus kind="alert">{summary}</AsyncStatus>
      )}
      <div>
        <label htmlFor={`${id}-server`}>Media server</label>
        <select
          id={`${id}-server`}
          value={serverId ?? ""}
          onChange={(event) => {
            setServerId(
              event.target.value === "" ? undefined : event.target.value,
            );
            setUserId("");
          }}
        >
          <option value="">Choose a server</option>
          {servers.map((server) => (
            <option key={server.id} value={server.id}>
              {server.name}
            </option>
          ))}
        </select>
      </div>
      <div>
        <label htmlFor={`${id}-user`}>Media-server user</label>
        <select
          id={`${id}-user`}
          value={userId}
          disabled={serverId === undefined || users.data === undefined}
          onChange={(event) => {
            setUserId(event.target.value);
          }}
        >
          <option value="">
            {serverId === undefined
              ? "Choose a server first"
              : users.status === "pending"
                ? "Loading users…"
                : users.status === "error"
                  ? "The users could not be loaded"
                  : users.data.items.length === 0
                    ? "No users on this server"
                    : "Choose a user"}
          </option>
          {(users.data?.items ?? []).map((user) => (
            <option key={user.id} value={user.id}>
              {user.name}
            </option>
          ))}
        </select>
      </div>
      <div className="form-actions">
        <button
          type="submit"
          className="btn-primary"
          disabled={serverId === undefined || userId === "" || link.isPending}
        >
          {link.isPending ? "Linking…" : "Link my account"}
        </button>
      </div>
    </form>
  );
}

function describeLinkError(error: unknown): string | undefined {
  if (error === null || error === undefined) {
    return undefined;
  }
  if (error instanceof ApiError) {
    switch (error.status) {
      case 409:
        return "That media-server user is already linked to another account.";
      case 404:
        return "That server or user no longer exists.";
      case 422:
        return "Choose a server and a user.";
      case 502:
      case 503:
        return "The media server could not be reached to confirm the user.";
      case undefined:
        break;
      default:
        break;
    }
  }
  return "The link could not be saved.";
}
