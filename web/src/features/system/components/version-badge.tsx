import type { ReactNode } from "react";
import { AsyncStatus } from "../../../components/async-status.js";
import { useVersion } from "../hooks/system-queries.js";

const SHORT_COMMIT_LENGTH = 7;

type VersionBadgeProps = Readonly<{
  // A quiet badge, as in the sidebar on every page, says nothing until the
  // version is known and nothing if it cannot be loaded: a live status on
  // every page load would only be noise. The About page shows both.
  quiet?: boolean;
}>;

export function VersionBadge({ quiet }: VersionBadgeProps): ReactNode {
  const query = useVersion();
  if (quiet === true && !query.isSuccess) {
    return null;
  }
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
