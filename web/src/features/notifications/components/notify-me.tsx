import type { ReactNode } from "react";
import { AsyncStatus } from "../../../components/async-status.js";
import { useSessionRecheck } from "../../auth/hooks/auth-queries.js";
import { accessDenial, ApiError } from "../../../lib/api/errors.js";
import { useSetTitleSubscription } from "../hooks/notifications-queries.js";

type NotifyMeProps = Readonly<{
  accountId: string;
  providerId: string;
  title: string;
  subscribed: boolean;
}>;

// One control on a title page: ask to be told when the title becomes
// available, or stop asking. The title's own flag is the source of truth;
// a success re-reads it, so the label follows the server, not a guess.
export function NotifyMe({
  accountId,
  providerId,
  title,
  subscribed,
}: NotifyMeProps): ReactNode {
  const set = useSetTitleSubscription(accountId);
  // A 401 or 403 means the session or the permission is gone: re-read it so
  // the guard acts, instead of leaving a control that can never succeed.
  useSessionRecheck(accessDenial(set.error) !== undefined, set.submittedAt);
  return (
    <div className="notify-me" aria-busy={set.isPending}>
      <button
        type="button"
        className={subscribed ? "" : "btn-primary"}
        aria-pressed={subscribed}
        disabled={set.isPending}
        onClick={() => {
          set.mutate({ providerId, subscribed: !subscribed });
        }}
      >
        {set.isPending
          ? "Saving…"
          : subscribed
            ? "Stop notifying me"
            : "Notify me when available"}
        <span className="visually-hidden"> about {title}</span>
      </button>
      <AsyncStatus kind={set.isError ? "alert" : "status"}>
        {set.isPending
          ? "Saving…"
          : set.isError
            ? describeSubscriptionError(set.error)
            : set.isSuccess
              ? subscribed
                ? "You will be told when it is available."
                : "You will not be notified about this title."
              : ""}
      </AsyncStatus>
    </div>
  );
}

function describeSubscriptionError(error: unknown): string {
  if (
    error instanceof ApiError &&
    error.code === "title_subscription_limit_exceeded"
  ) {
    return "You already follow 500 titles; stop following one first.";
  }
  return "That could not be saved. Please try again.";
}
