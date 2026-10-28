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
import type { MetadataKeyProvider } from "../api/requests-api.js";
import { metadataKeyRequestSchema } from "../api/requests-schemas.js";
import type {
  useMetadataKeyPresence,
  useRemoveMetadataKey,
  useSetMetadataKey,
} from "../hooks/requests-queries.js";
import { ConfirmControls } from "./confirm-controls.js";
import { ActionFailed, RetryButton } from "./list-states.js";
import { describeKeyError } from "./request-errors.js";

type KeySectionProps = Readonly<{
  presence: ReturnType<typeof useMetadataKeyPresence>;
  set: ReturnType<typeof useSetMetadataKey>;
  remove: ReturnType<typeof useRemoveMetadataKey>;
}>;

// The words that differ between providers. `noun` is "token" or "key";
// `name` names the stored secret, such as "TMDB token".
type KeyCopy = Readonly<{
  provider: MetadataKeyProvider;
  heading: string;
  intro: string;
  noun: string;
  name: string;
  label: string;
  required: string;
  // A refusal for a value that is plainly the wrong kind of key, if any.
  misfit: (value: string) => string | undefined;
}>;

const TMDB_COPY: KeyCopy = {
  provider: "tmdb",
  heading: "TMDB API Read Access Token",
  intro:
    "Searching for titles and creating requests needs The Movie Database's API Read Access Token, shown under Settings, API on your TMDB account. The token is stored encrypted and is never shown again. The shorter v3 API key is not accepted.",
  noun: "token",
  name: "TMDB token",
  label: "API Read Access Token",
  required: "Enter the TMDB API Read Access Token.",
  // TMDB's v3 key is 32 hex characters; the v4 Read Access Token is a much
  // longer three-part token. Only the unmistakable v3 shape is refused here;
  // anything else is the server's call.
  misfit: (value) =>
    /^[0-9a-f]{32}$/i.test(value)
      ? "That is a v3 API key. Bloom needs the API Read Access Token from TMDB's API settings."
      : undefined,
};

const OMDB_COPY: KeyCopy = {
  provider: "omdb",
  heading: "OMDb API key",
  intro:
    "Title pages show IMDb, Rotten Tomatoes, and Metacritic scores when an OMDb API key is stored. Bloom checks the key with OMDb, stores it encrypted, and never shows it again. Without a key, title pages show only TMDB's score.",
  noun: "key",
  name: "OMDb key",
  label: "OMDb API key",
  required: "Enter the OMDb API key.",
  misfit: (value) =>
    /^[A-Za-z0-9]{8,64}$/.test(value)
      ? undefined
      : "An OMDb API key is 8 to 64 letters and digits.",
};

// Whether a TMDB token is stored, and the two things that can change that.
// The token itself never comes back from the server and is cleared from the
// form as soon as it has been sent.
export function TmdbKeySection(props: KeySectionProps): ReactNode {
  return <ProviderKeySection {...props} copy={TMDB_COPY} />;
}

// The same for the optional OMDb key behind outside ratings.
export function OmdbKeySection(props: KeySectionProps): ReactNode {
  return <ProviderKeySection {...props} copy={OMDB_COPY} />;
}

function ProviderKeySection({
  presence,
  set,
  remove,
  copy,
}: KeySectionProps & Readonly<{ copy: KeyCopy }>): ReactNode {
  const headingId = `${copy.provider}-key-heading`;
  return (
    <section aria-labelledby={headingId} className="card panel">
      <h2 id={headingId}>{copy.heading}</h2>
      <p>{copy.intro}</p>
      <Presence presence={presence} remove={remove} copy={copy} />
      <KeyForm
        set={set}
        configured={presence.data?.configured === true}
        copy={copy}
      />
    </section>
  );
}

function Presence({
  presence,
  remove,
  copy,
}: Readonly<{
  presence: ReturnType<typeof useMetadataKeyPresence>;
  remove: ReturnType<typeof useRemoveMetadataKey>;
  copy: KeyCopy;
}>): ReactNode {
  if (presence.status === "pending") {
    return <AsyncStatus>Checking for a stored {copy.noun}…</AsyncStatus>;
  }
  if (presence.status === "error") {
    return (
      <>
        <AsyncStatus kind="alert">
          Whether a {copy.noun} is stored could not be checked.
        </AsyncStatus>
        <RetryButton
          retrying={presence.isRefetching}
          onRetry={presence.refetch}
        />
      </>
    );
  }
  const configured = presence.data.configured;
  return (
    <>
      <ActionFailed summary={describeKeyError(remove.error, copy.provider)} />
      <p className="key-presence">
        <span
          className={configured ? "badge badge-success" : "badge badge-warning"}
        >
          {configured
            ? `${capitalize(copy.noun)} stored`
            : `No ${copy.noun} stored`}
        </span>
      </p>
      {configured ? (
        <ConfirmControls
          action="Remove"
          confirming="removing"
          subject={`the ${copy.name}`}
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
  copy,
}: Readonly<{
  set: ReturnType<typeof useSetMetadataKey>;
  configured: boolean;
  copy: KeyCopy;
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
        copy={copy}
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
      <p role="status">{saved ? `The ${copy.name} was saved.` : ""}</p>
    </>
  );
}

function KeyFields({
  pending,
  serverError,
  configured,
  copy,
  onSubmit,
}: Readonly<{
  pending: boolean;
  serverError: unknown;
  configured: boolean;
  copy: KeyCopy;
  onSubmit: (apiKey: string) => void;
}>): ReactNode {
  const formId = useId();
  const summaryRef = useRef<HTMLParagraphElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const [error, setError] = useState<string | undefined>(undefined);
  const summary = describeKeyError(serverError, copy.provider);
  useEffect(() => {
    if (summary !== undefined) {
      summaryRef.current?.focus();
    }
  }, [summary]);

  function refuse(message: string): void {
    setError(message);
    inputRef.current?.focus();
  }

  function handleSubmit(event: SyntheticEvent<HTMLFormElement>): void {
    event.preventDefault();
    if (pending) {
      return;
    }
    const value = new FormData(event.currentTarget).get("api_key");
    const trimmed = typeof value === "string" ? value.trim() : "";
    const parsed = metadataKeyRequestSchema.safeParse({ api_key: trimmed });
    if (!parsed.success) {
      refuse(copy.required);
      return;
    }
    const misfit = copy.misfit(trimmed);
    if (misfit !== undefined) {
      refuse(misfit);
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
      aria-label={
        configured ? `Replace the ${copy.name}` : `Store the ${copy.name}`
      }
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
        <label htmlFor={id}>
          {configured ? `New ${copy.label}` : copy.label}
        </label>
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
        {pending
          ? "Saving…"
          : configured
            ? `Replace ${copy.noun}`
            : `Store ${copy.noun}`}
      </button>
      <p role="status">{pending ? `Saving the ${copy.name}.` : ""}</p>
    </form>
  );
}

function capitalize(word: string): string {
  return word.charAt(0).toUpperCase() + word.slice(1);
}
