import { useEffect, useRef, type ReactNode } from "react";
import { AsyncStatus } from "../../../components/async-status.js";

type RetryProps = Readonly<{ onRetry: () => Promise<unknown> }>;

// The states every paged admin list shares: a failed first load, a failed
// refresh that keeps the rows, a failed action, and the pager.
export function LoadFailed({
  noun,
  onRetry,
}: RetryProps & Readonly<{ noun: string }>): ReactNode {
  return (
    <>
      <AsyncStatus kind="alert">The {noun} could not be loaded.</AsyncStatus>
      <RetryButton retrying={false} onRetry={onRetry} />
    </>
  );
}

export function RefreshFailed({
  noun,
  retrying,
  onRetry,
}: RetryProps & Readonly<{ noun: string; retrying: boolean }>): ReactNode {
  return (
    <div className="stale-warning">
      <AsyncStatus kind="alert">
        The {noun} could not be refreshed. What is shown may be out of date.
      </AsyncStatus>
      <RetryButton retrying={retrying} onRetry={onRetry} />
    </div>
  );
}

export function RetryButton({
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

// A failed action replaced the focused control with nothing, so the
// explanation takes focus itself.
export function ActionFailed({
  summary,
}: Readonly<{ summary: string | undefined }>): ReactNode {
  const ref = useRef<HTMLParagraphElement>(null);
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

type PagerProps = Readonly<{
  noun: string;
  hasMore: boolean;
  loading: boolean;
  failed: boolean;
  onMore: () => Promise<unknown>;
}>;

export function Pager({
  noun,
  hasMore,
  loading,
  failed,
  onMore,
}: PagerProps): ReactNode {
  if (!hasMore) {
    return null;
  }
  return (
    <div className="pager">
      {failed ? <p role="alert">More {noun} could not be loaded.</p> : null}
      <button
        type="button"
        disabled={loading}
        onClick={() => {
          void onMore();
        }}
      >
        {loading ? "Loading more…" : `Load more ${noun}`}
      </button>
      <span role="status">{loading ? `Loading more ${noun}.` : ""}</span>
    </div>
  );
}

export function SignInNotConfirmed({
  noun,
  onRetry,
}: RetryProps & Readonly<{ noun: string }>): ReactNode {
  return (
    <>
      <AsyncStatus kind="alert">
        The {noun} could not be loaded because your sign-in could not be
        confirmed.
      </AsyncStatus>
      <RetryButton retrying={false} onRetry={onRetry} />
    </>
  );
}
