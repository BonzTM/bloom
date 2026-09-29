import { useId, useState, type ReactNode, type SyntheticEvent } from "react";
import { AsyncStatus } from "../components/async-status.js";
import {
  useSession,
  useSessionRecheck,
} from "../features/auth/hooks/auth-queries.js";
import {
  notificationEventTypeSchema,
  type NotificationEventType,
  type NotificationPreferences,
} from "../features/notifications/api/notification-schemas.js";
import {
  usePreferences,
  useReplacePreferences,
} from "../features/notifications/hooks/notifications-queries.js";
import { LoadFailed } from "../features/requests/components/list-states.js";
import { accessDenial } from "../lib/api/errors.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

// What each account is told about, one switch per event kind. Delivery
// itself goes through the channels the operator set up.
export default function MyNotificationsRoute(): ReactNode {
  usePageTitle(pageTitle("My notifications"));
  const session = useSession();
  const accountId = session.data?.account.id;
  if (accountId === undefined) {
    return null;
  }
  return <MyNotificationsPage key={accountId} accountId={accountId} />;
}

function MyNotificationsPage({
  accountId,
}: Readonly<{ accountId: string }>): ReactNode {
  const preferences = usePreferences(accountId);
  const replace = useReplacePreferences(accountId);
  const denial = accessDenial(preferences.error) ?? accessDenial(replace.error);
  useSessionRecheck(
    denial !== undefined,
    Math.max(preferences.errorUpdatedAt, replace.submittedAt),
  );
  return (
    <>
      <h1>My notifications</h1>
      <p className="page-intro">
        Choose what Bloom tells you about. Messages arrive through the channels
        your administrator set up; you cannot see or change those here.
      </p>
      <section aria-labelledby="preferences-heading" className="card">
        <h2 id="preferences-heading">What to tell you about</h2>
        {preferences.status === "pending" ? (
          <AsyncStatus>Loading your preferences…</AsyncStatus>
        ) : preferences.status === "error" ? (
          denial === "unauthenticated" ? (
            <AsyncStatus kind="alert">
              Your preferences could not be loaded because your sign-in could
              not be confirmed.
            </AsyncStatus>
          ) : (
            <LoadFailed noun="preferences" onRetry={preferences.refetch} />
          )
        ) : (
          <PreferencesForm
            stored={preferences.data}
            pending={replace.isPending}
            saved={replace.isSuccess}
            failed={replace.isError}
            onSubmit={(input) => {
              replace.mutate(input);
            }}
          />
        )}
      </section>
    </>
  );
}

const EVENT_LABELS: Readonly<
  Record<NotificationEventType, readonly [string, string]>
> = {
  created: ["Request made", "A request of yours was recorded."],
  approved: ["Request approved", "An administrator approved your request."],
  declined: ["Request declined", "An administrator declined your request."],
  dispatched: [
    "Download started",
    "Your request was handed to a download manager.",
  ],
  available: [
    "Available to watch",
    "A title you requested or follow is on the server.",
  ],
  failed: ["Request failed", "A download for your request could not finish."],
  "playback.session_started": [
    "Playback started",
    "Someone started watching on a server you can see.",
  ],
};

type PreferencesFormProps = Readonly<{
  stored: NotificationPreferences;
  pending: boolean;
  saved: boolean;
  failed: boolean;
  onSubmit: (input: NotificationPreferences) => void;
}>;

// One checkbox per event kind. The whole matrix is sent on save, in the
// contract's order, so the server always receives every kind once.
function PreferencesForm({
  stored,
  pending,
  saved,
  failed,
  onSubmit,
}: PreferencesFormProps): ReactNode {
  const id = useId();
  const [draft, setDraft] = useState(() => toMap(stored));
  function handleSubmit(event: SyntheticEvent<HTMLFormElement>): void {
    event.preventDefault();
    onSubmit(
      notificationEventTypeSchema.options.map((event_type) => ({
        event_type,
        enabled: draft.get(event_type) ?? true,
      })),
    );
  }
  return (
    <form
      className="preferences-form"
      aria-label="Notification preferences"
      onSubmit={handleSubmit}
      noValidate
    >
      <fieldset className="field">
        <legend>Tell me when</legend>
        {notificationEventTypeSchema.options.map((eventType) => {
          const [label, detail] = EVENT_LABELS[eventType];
          return (
            <label
              key={eventType}
              className="choice choice-detailed"
              htmlFor={`${id}-${eventType}`}
            >
              <input
                id={`${id}-${eventType}`}
                type="checkbox"
                checked={draft.get(eventType) ?? true}
                onChange={(event) => {
                  const next = new Map(draft);
                  next.set(eventType, event.target.checked);
                  setDraft(next);
                }}
              />
              <span>
                {label}
                <span className="row-detail">{detail}</span>
              </span>
            </label>
          );
        })}
      </fieldset>
      <div className="form-actions">
        <button type="submit" className="btn-primary" disabled={pending}>
          {pending ? "Saving…" : "Save preferences"}
        </button>
      </div>
      <AsyncStatus kind={failed ? "alert" : "status"}>
        {failed
          ? "Your preferences could not be saved. Please try again."
          : saved
            ? "Preferences saved."
            : ""}
      </AsyncStatus>
    </form>
  );
}

function toMap(
  stored: NotificationPreferences,
): Map<NotificationEventType, boolean> {
  return new Map(stored.map((item) => [item.event_type, item.enabled]));
}
