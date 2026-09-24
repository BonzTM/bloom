import { useState, type ReactNode } from "react";
import { AsyncStatus } from "../../../components/async-status.js";
import { ApiError } from "../../../lib/api/errors.js";
import type { RequestProgress as Progress } from "../api/download-manager-schemas.js";
import { useRequestProgress } from "../hooks/requests-queries.js";

type RequestProgressProps = Readonly<{
  accountId: string;
  requestId: string;
  title: string;
}>;

// Live queue state for a processing request, read only when asked for.
export function RequestProgress({
  accountId,
  requestId,
  title,
}: RequestProgressProps): ReactNode {
  const [open, setOpen] = useState(false);
  if (!open) {
    return (
      <button
        type="button"
        onClick={() => {
          setOpen(true);
        }}
      >
        Progress <span className="visually-hidden">of {title}</span>
      </button>
    );
  }
  return (
    <ProgressDetail accountId={accountId} requestId={requestId} title={title} />
  );
}

function ProgressDetail({
  accountId,
  requestId,
  title,
}: RequestProgressProps): ReactNode {
  const progress = useRequestProgress(accountId, requestId);
  if (progress.status === "pending") {
    return <AsyncStatus>Reading the queue…</AsyncStatus>;
  }
  if (progress.status === "error") {
    return (
      <span role="status" className="row-detail">
        {describeProgressError(progress.error)}
      </span>
    );
  }
  return (
    <span className="row-detail" aria-label={`Progress of ${title}`}>
      {describeProgress(progress.data)}
    </span>
  );
}

export function describeProgress(progress: Progress): string {
  const done =
    progress.size === 0
      ? 0
      : Math.round(
          ((progress.size - progress.size_left) / progress.size) * 100,
        );
  const parts = [`${progress.status}, ${String(done)}% done`];
  if (progress.estimated_completion !== undefined) {
    parts.push(
      `expected ${progress.estimated_completion.slice(0, 16).replace("T", " ")}`,
    );
  }
  return parts.join(", ");
}

function describeProgressError(error: unknown): string {
  if (error instanceof ApiError && error.status === 404) {
    return "Not in a download queue yet.";
  }
  if (
    error instanceof ApiError &&
    (error.status === 502 || error.status === 503)
  ) {
    return "The download manager did not answer.";
  }
  return "Progress could not be read.";
}
