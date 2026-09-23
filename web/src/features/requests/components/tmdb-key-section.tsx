import {
  useEffect,
  useId,
  useRef,
  useState,
  type ReactNode,
  type SyntheticEvent,
} from "react";
import { AsyncStatus } from "../../../components/async-status.js";
import { FieldError, fieldErrorId } from "../../auth/components/field-error.js";
import { metadataKeyRequestSchema } from "../api/requests-schemas.js";
import type {
  useMetadataKeyPresence,
  useRemoveMetadataKey,
  useSetMetadataKey,
} from "../hooks/requests-queries.js";
import { ConfirmControls } from "./confirm-controls.js";
import { ActionFailed, RetryButton } from "./list-states.js";
import { describeKeyError } from "./request-errors.js";

type TmdbKeySectionProps = Readonly<{
  presence: ReturnType<typeof useMetadataKeyPresence>;
  set: ReturnType<typeof useSetMetadataKey>;
  remove: ReturnType<typeof useRemoveMetadataKey>;
}>;

// Whether a TMDB key is stored, and the two things that can change that.
// The key itself never comes back from the server and is cleared from the
// form as soon as it has been sent.
export function TmdbKeySection({
  presence,
  set,
  remove,
}: TmdbKeySectionProps): ReactNode {
  return (
    <section aria-labelledby="tmdb-key-heading" className="card">
      <h2 id="tmdb-key-heading">TMDB API key</h2>
      <p>
        Searching for titles and creating requests needs a key from The Movie
        Database. The key is stored encrypted and is never shown again.
      </p>
      <Presence presence={presence} remove={remove} />
      <KeyForm set={set} configured={presence.data?.configured === true} />
    </section>
  );
}

function Presence({
  presence,
  remove,
}: Readonly<{
  presence: ReturnType<typeof useMetadataKeyPresence>;
  remove: ReturnType<typeof useRemoveMetadataKey>;
}>): ReactNode {
  if (presence.status === "pending") {
    return <AsyncStatus>Checking for a stored key…</AsyncStatus>;
  }
  if (presence.status === "error") {
    return (
      <>
        <AsyncStatus kind="alert">
          Whether a key is stored could not be checked.
        </AsyncStatus>
        <RetryButton
          retrying={presence.isRefetching}
          onRetry={presence.refetch}
        />
      </>
    );
  }
  return (
    <>
      <ActionFailed summary={describeKeyError(remove.error)} />
      <p className="key-presence">
        <span
          className={
            presence.data.configured
              ? "badge badge-success"
              : "badge badge-warning"
          }
        >
          {presence.data.configured ? "Key stored" : "No key stored"}
        </span>
      </p>
      {presence.data.configured ? (
        <ConfirmControls
          action="Remove"
          confirming="removing"
          subject="the TMDB key"
          busy={remove.isPending}
          busyLabel="Removing…"
          onConfirm={() => {
            remove.mutate();
          }}
        />
      ) : null}
    </>
  );
}

// Uncontrolled: the browser holds the typed key until submit, and the form
// is remounted after a success so the key does not stay on screen.
function KeyForm({
  set,
  configured,
}: Readonly<{
  set: ReturnType<typeof useSetMetadataKey>;
  configured: boolean;
}>): ReactNode {
  const [formKey, setFormKey] = useState(0);
  const [saved, setSaved] = useState(false);
  return (
    <>
      <KeyFields
        key={formKey}
        pending={set.isPending}
        serverError={set.error}
        configured={configured}
        onSubmit={(apiKey) => {
          setSaved(false);
          set.setKey(
            { api_key: apiKey },
            {
              onSuccess: () => {
                setSaved(true);
                setFormKey((current) => current + 1);
              },
            },
          );
        }}
      />
      <p role="status">{saved ? "The TMDB key was saved." : ""}</p>
    </>
  );
}

function KeyFields({
  pending,
  serverError,
  configured,
  onSubmit,
}: Readonly<{
  pending: boolean;
  serverError: unknown;
  configured: boolean;
  onSubmit: (apiKey: string) => void;
}>): ReactNode {
  const formId = useId();
  const summaryRef = useRef<HTMLParagraphElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const [error, setError] = useState<string | undefined>(undefined);
  const summary = describeKeyError(serverError);
  useEffect(() => {
    if (summary !== undefined) {
      summaryRef.current?.focus();
    }
  }, [summary]);

  function handleSubmit(event: SyntheticEvent<HTMLFormElement>): void {
    event.preventDefault();
    if (pending) {
      return;
    }
    const value = new FormData(event.currentTarget).get("api_key");
    const parsed = metadataKeyRequestSchema.safeParse({
      api_key: typeof value === "string" ? value.trim() : "",
    });
    if (!parsed.success) {
      setError("Enter the TMDB API key.");
      inputRef.current?.focus();
      return;
    }
    setError(undefined);
    onSubmit(parsed.data.api_key);
  }

  const id = `${formId}-api_key`;
  const summaryId = `${formId}-summary`;
  return (
    <form
      onSubmit={handleSubmit}
      aria-label={configured ? "Replace the TMDB key" : "Store the TMDB key"}
      noValidate
      aria-busy={pending}
      aria-describedby={summary === undefined ? undefined : summaryId}
    >
      {summary === undefined ? null : (
        <p id={summaryId} role="alert" tabIndex={-1} ref={summaryRef}>
          {summary}
        </p>
      )}
      <div>
        <label htmlFor={id}>{configured ? "New API key" : "API key"}</label>
        <input
          ref={inputRef}
          id={id}
          name="api_key"
          type="password"
          autoComplete="off"
          spellCheck={false}
          required
          aria-invalid={error !== undefined}
          aria-describedby={fieldErrorId(formId, "api_key")}
        />
        <FieldError formId={formId} field="api_key" message={error} />
      </div>
      <button type="submit" className="btn-primary" disabled={pending}>
        {pending ? "Saving…" : configured ? "Replace key" : "Store key"}
      </button>
      <p role="status">{pending ? "Saving the TMDB key." : ""}</p>
    </form>
  );
}
