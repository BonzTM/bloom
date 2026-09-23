import {
  useEffect,
  useId,
  useRef,
  useState,
  type ReactNode,
  type SyntheticEvent,
} from "react";
import { Link } from "react-router-dom";
import { FieldError, fieldErrorId } from "../../auth/components/field-error.js";
import {
  createMediaRequestSchema,
  type CreateMediaRequest,
  type MetadataSeason,
  type MetadataTitle,
} from "../api/metadata-schemas.js";
import type { RequestProfile } from "../api/requests-schemas.js";
import { describeCreateError } from "./request-errors.js";

type RequestFormProps = Readonly<{
  title: MetadataTitle;
  // Seasons a series offers; undefined for a movie.
  seasons: readonly MetadataSeason[] | undefined;
  profiles: readonly RequestProfile[];
  pending: boolean;
  serverError: unknown;
  onSubmit: (input: CreateMediaRequest) => void;
}>;

type Field = "profile_id" | "seasons";

type FieldErrors = Partial<Record<Field, string>>;

// Uncontrolled on purpose: the browser keeps the chosen profile and seasons,
// so a rejected submit never loses them.
export function RequestForm({
  title,
  seasons,
  profiles,
  pending,
  serverError,
  onSubmit,
}: RequestFormProps): ReactNode {
  const formId = useId();
  const summaryRef = useRef<HTMLParagraphElement>(null);
  const profileRef = useRef<HTMLSelectElement>(null);
  const seasonsRef = useRef<HTMLInputElement>(null);
  const [errors, setErrors] = useState<FieldErrors>({});
  const summary = describeCreateError(serverError);
  const choices = profiles.filter((profile) =>
    profile.kinds.includes(title.kind),
  );
  const requestable = (seasons ?? []).filter((season) => season.number >= 1);

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
    const result = readInput(new FormData(event.currentTarget), title);
    if (result.errors !== undefined) {
      setErrors(result.errors);
      (result.errors.profile_id === undefined
        ? seasonsRef
        : profileRef
      ).current?.focus();
      return;
    }
    setErrors({});
    onSubmit(result.input);
  }

  if (choices.length === 0) {
    return (
      <p role="status">
        No request profile accepts{" "}
        {title.kind === "movie" ? "movies" : "series"} yet, so this title cannot
        be requested. Ask an administrator to add one.
      </p>
    );
  }
  const summaryId = `${formId}-summary`;
  return (
    <form
      onSubmit={handleSubmit}
      aria-label={`Request ${title.title}`}
      noValidate
      aria-busy={pending}
      aria-describedby={summary === undefined ? undefined : summaryId}
    >
      {summary === undefined ? null : (
        <p id={summaryId} role="alert" tabIndex={-1} ref={summaryRef}>
          {summary}
          {isQuota(serverError) ? (
            <>
              {" "}
              <Link to="/requests">See your requests.</Link>
            </>
          ) : null}
        </p>
      )}
      <ProfileField
        formId={formId}
        choices={choices}
        error={errors.profile_id}
        selectRef={profileRef}
      />
      {seasons === undefined ? null : (
        <SeasonsField
          formId={formId}
          seasons={requestable}
          error={errors.seasons}
          inputRef={seasonsRef}
        />
      )}
      <button type="submit" className="btn-primary" disabled={pending}>
        {pending ? "Requesting…" : "Request"}
      </button>
      <p role="status">{pending ? "Sending your request." : ""}</p>
    </form>
  );
}

function isQuota(error: unknown): boolean {
  return (
    typeof error === "object" &&
    error !== null &&
    "code" in error &&
    error.code === "request_quota_exceeded"
  );
}

type ReadResult =
  | Readonly<{ input: CreateMediaRequest; errors?: undefined }>
  | Readonly<{ input?: undefined; errors: FieldErrors }>;

function readInput(data: FormData, title: MetadataTitle): ReadResult {
  const errors: FieldErrors = {};
  const profileId = data.get("profile_id");
  if (typeof profileId !== "string" || profileId === "") {
    errors.profile_id = "Choose a profile for this request.";
  }
  const seasons = data
    .getAll("seasons")
    .filter((value): value is string => typeof value === "string")
    .map(Number);
  if (title.kind === "series" && seasons.length === 0) {
    errors.seasons = "Choose at least one season.";
  }
  if (Object.keys(errors).length > 0) {
    return { errors };
  }
  const parsed = createMediaRequestSchema.safeParse({
    kind: title.kind,
    provider_id: title.provider_id,
    profile_id: profileId,
    seasons: title.kind === "movie" ? [] : seasons.sort((a, b) => a - b),
  });
  if (!parsed.success) {
    return { errors: { seasons: "Choose valid seasons." } };
  }
  return { input: parsed.data };
}

function ProfileField({
  formId,
  choices,
  error,
  selectRef,
}: Readonly<{
  formId: string;
  choices: readonly RequestProfile[];
  error: string | undefined;
  selectRef: React.RefObject<HTMLSelectElement | null>;
}>): ReactNode {
  const id = `${formId}-profile_id`;
  return (
    <div>
      <label htmlFor={id}>Profile</label>
      <select
        ref={selectRef}
        id={id}
        name="profile_id"
        required
        defaultValue={choices.length === 1 ? choices[0]?.id : ""}
        aria-invalid={error !== undefined}
        aria-describedby={fieldErrorId(formId, "profile_id")}
      >
        {choices.length === 1 ? null : (
          <option value="">Choose a profile</option>
        )}
        {choices.map((profile) => (
          <option key={profile.id} value={profile.id}>
            {profile.name}
          </option>
        ))}
      </select>
      <FieldError formId={formId} field="profile_id" message={error} />
    </div>
  );
}

function SeasonsField({
  formId,
  seasons,
  error,
  inputRef,
}: Readonly<{
  formId: string;
  seasons: readonly MetadataSeason[];
  error: string | undefined;
  inputRef: React.RefObject<HTMLInputElement | null>;
}>): ReactNode {
  return (
    <fieldset
      className="checkbox-group"
      aria-invalid={error !== undefined}
      aria-describedby={fieldErrorId(formId, "seasons")}
    >
      <legend>Seasons</legend>
      {seasons.map((season, index) => {
        const id = `${formId}-season-${String(season.number)}`;
        return (
          <div key={season.number} className="checkbox-field">
            <label htmlFor={id}>
              <input
                ref={index === 0 ? inputRef : undefined}
                id={id}
                name="seasons"
                type="checkbox"
                value={season.number}
              />{" "}
              {season.name}
              <span className="row-detail">
                {String(season.episode_count)} episodes
              </span>
            </label>
          </div>
        );
      })}
      <FieldError formId={formId} field="seasons" message={error} />
    </fieldset>
  );
}
