import type { ReactNode } from "react";

type SessionUnavailableProps = Readonly<{
  onRetry: () => Promise<unknown>;
}>;

// The first session check failed and nothing is known about the visitor.
export function SessionUnavailable({
  onRetry,
}: SessionUnavailableProps): ReactNode {
  return (
    <>
      <span role="alert">Sign-in status is unavailable.</span>
      <button
        type="button"
        onClick={() => {
          void onRetry();
        }}
      >
        Retry
      </button>
    </>
  );
}
