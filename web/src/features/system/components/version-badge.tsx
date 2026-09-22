import type { ReactNode } from "react";
import { AsyncStatus } from "../../../components/async-status.js";
import { useVersion } from "../hooks/system-queries.js";

const SHORT_COMMIT_LENGTH = 7;

export function VersionBadge(): ReactNode {
  const query = useVersion();
  if (query.isPending) {
    return <AsyncStatus>Loading server version…</AsyncStatus>;
  }
  if (query.isError) {
    return (
      <AsyncStatus kind="alert">
        The server version could not be loaded.
      </AsyncStatus>
    );
  }
  return (
    <p className="version-badge">
      Version {query.data.version} (commit{" "}
      <code>{formatCommit(query.data.commit)}</code>)
    </p>
  );
}

function formatCommit(commit: string): string {
  if (commit.length === 0) {
    return "unknown";
  }
  return commit.slice(0, SHORT_COMMIT_LENGTH);
}
