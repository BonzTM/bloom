import {
  useEffect,
  useId,
  useRef,
  useState,
  type ReactNode,
  type RefObject,
  type SyntheticEvent,
} from "react";
import { FieldError, fieldErrorId } from "../../auth/components/field-error.js";
import {
  mediaKindSchema,
  type RequestProfile,
  type RequestProfileInput,
} from "../api/requests-schemas.js";
import {
  firstInvalidProfileField,
  readProfileInput,
  type ProfileField,
  type ProfileFieldErrors,
} from "./profile-input.js";
import { kindLabel } from "./request-format.js";
import { describeProfileError } from "./request-errors.js";

type RequestProfileFormProps = Readonly<{
  // The profile being edited; absent when creating a new one.
  profile: RequestProfile | undefined;
  pending: boolean;
  serverError: unknown;
  onSubmit: (input: RequestProfileInput) => void;
  onCancel: (() => void) | undefined;
}>;

const DOWNLOAD_MANAGER_KINDS = [
  { value: "radarr", label: "Radarr" },
  { value: "sonarr", label: "Sonarr" },
] as const;

// Uncontrolled on purpose: the browser keeps the typed values, so a rejected
// submit never loses input. The route remounts the form after a success or
// when a different profile is chosen for editing.
export function RequestProfileForm({
  profile,
  pending,
  serverError,
  onSubmit,
  onCancel,
}: RequestProfileFormProps): ReactNode {
  const formId = useId();
  const summaryRef = useRef<HTMLParagraphElement>(null);
  const refs = useFieldRefs();
  const [errors, setErrors] = useState<ProfileFieldErrors>({});
  const summary = describeProfileError(serverError);
  const editing = profile !== undefined;

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
    const result = readProfileInput(new FormData(event.currentTarget));
    if (result.errors !== undefined) {
      setErrors(result.errors);
      focusFirstInvalid(result.errors, refs);
      return;
    }
    setErrors({});
    onSubmit(result.input);
  }

  const summaryId = `${formId}-summary`;
  return (
    <form
      onSubmit={handleSubmit}
      aria-label={editing ? `Edit profile ${profile.name}` : "Create a profile"}
      noValidate
      aria-busy={pending}
      aria-describedby={summary === undefined ? undefined : summaryId}
    >
      {summary === undefined ? null : (
        <p id={summaryId} role="alert" tabIndex={-1} ref={summaryRef}>
          {summary}
        </p>
      )}
      <TextField
        formId={formId}
        name="name"
        label="Name"
        defaultValue={profile?.name}
        error={errors.name}
        inputRef={refs.name}
      />
      <KindsField
        formId={formId}
        defaultKinds={profile?.kinds ?? []}
        error={errors.kinds}
        inputRef={refs.kinds}
      />
      <ManagerKindField
        formId={formId}
        defaultValue={profile?.download_manager_kind}
        error={errors.download_manager_kind}
        selectRef={refs.download_manager_kind}
      />
      <TextField
        formId={formId}
        name="download_manager_instance"
        label="Download manager instance"
        hint="The name of the Radarr or Sonarr instance that receives approved requests."
        defaultValue={profile?.download_manager_instance}
        error={errors.download_manager_instance}
        inputRef={refs.download_manager_instance}
      />
      <TextField
        formId={formId}
        name="quality_profile"
        label="Quality profile"
        defaultValue={profile?.quality_profile}
        error={errors.quality_profile}
        inputRef={refs.quality_profile}
      />
      <TextField
        formId={formId}
        name="root_folder"
        label="Root folder"
        defaultValue={profile?.root_folder}
        error={errors.root_folder}
        inputRef={refs.root_folder}
      />
      <TextField
        formId={formId}
        name="tags"
        label="Tags"
        hint="Comma-separated. Optional."
        required={false}
        defaultValue={profile?.tags.join(", ")}
        error={errors.tags}
        inputRef={refs.tags}
      />
      <div className="form-actions">
        <button type="submit" className="btn-primary" disabled={pending}>
          {submitLabel(editing, pending)}
        </button>
        {onCancel === undefined ? null : (
          <button type="button" className="btn-ghost" onClick={onCancel}>
            Cancel editing
          </button>
        )}
      </div>
      <p role="status">{pending ? "Saving the profile." : ""}</p>
    </form>
  );
}

function submitLabel(editing: boolean, pending: boolean): string {
  if (pending) {
    return "Saving…";
  }
  return editing ? "Save profile" : "Create profile";
}

type FieldRefs = Readonly<{
  name: RefObject<HTMLInputElement | null>;
  kinds: RefObject<HTMLInputElement | null>;
  download_manager_kind: RefObject<HTMLSelectElement | null>;
  download_manager_instance: RefObject<HTMLInputElement | null>;
  quality_profile: RefObject<HTMLInputElement | null>;
  root_folder: RefObject<HTMLInputElement | null>;
  tags: RefObject<HTMLInputElement | null>;
}>;

function useFieldRefs(): FieldRefs {
  return {
    name: useRef<HTMLInputElement>(null),
    kinds: useRef<HTMLInputElement>(null),
    download_manager_kind: useRef<HTMLSelectElement>(null),
    download_manager_instance: useRef<HTMLInputElement>(null),
    quality_profile: useRef<HTMLInputElement>(null),
    root_folder: useRef<HTMLInputElement>(null),
    tags: useRef<HTMLInputElement>(null),
  };
}

// A rejected submit moves focus to the first control that needs attention,
// in the order the fields appear on screen.
function focusFirstInvalid(errors: ProfileFieldErrors, refs: FieldRefs): void {
  const first: ProfileField | undefined = firstInvalidProfileField(errors);
  if (first !== undefined) {
    refs[first].current?.focus();
  }
}

type TextFieldProps = Readonly<{
  formId: string;
  name: Exclude<ProfileField, "kinds" | "download_manager_kind">;
  label: string;
  hint?: string;
  required?: boolean;
  defaultValue: string | undefined;
  error: string | undefined;
  inputRef: RefObject<HTMLInputElement | null>;
}>;

function TextField({
  formId,
  name,
  label,
  hint,
  required = true,
  defaultValue,
  error,
  inputRef,
}: TextFieldProps): ReactNode {
  const id = `${formId}-${name}`;
  const hintId = `${id}-hint`;
  const describedBy =
    hint === undefined
      ? fieldErrorId(formId, name)
      : `${hintId} ${fieldErrorId(formId, name)}`;
  return (
    <div>
      <label htmlFor={id}>{label}</label>
      {hint === undefined ? null : (
        <p id={hintId} className="field-hint">
          {hint}
        </p>
      )}
      <input
        ref={inputRef}
        id={id}
        name={name}
        type="text"
        autoComplete="off"
        spellCheck={false}
        required={required}
        defaultValue={defaultValue}
        aria-invalid={error !== undefined}
        aria-describedby={describedBy}
      />
      <FieldError formId={formId} field={name} message={error} />
    </div>
  );
}

type KindsFieldProps = Readonly<{
  formId: string;
  defaultKinds: readonly string[];
  error: string | undefined;
  inputRef: RefObject<HTMLInputElement | null>;
}>;

// One checkbox per media kind; the first one carries the focus target for a
// rejected submit.
function KindsField({
  formId,
  defaultKinds,
  error,
  inputRef,
}: KindsFieldProps): ReactNode {
  const errorId = fieldErrorId(formId, "kinds");
  return (
    <fieldset
      className="checkbox-group"
      aria-invalid={error !== undefined}
      aria-describedby={errorId}
    >
      <legend>Media kinds</legend>
      {mediaKindSchema.options.map((kind, index) => {
        const id = `${formId}-kinds-${kind}`;
        return (
          <div key={kind} className="checkbox-field">
            <label htmlFor={id}>
              <input
                ref={index === 0 ? inputRef : undefined}
                id={id}
                name="kinds"
                type="checkbox"
                value={kind}
                defaultChecked={defaultKinds.includes(kind)}
              />{" "}
              {kindLabel(kind)}
            </label>
          </div>
        );
      })}
      <FieldError formId={formId} field="kinds" message={error} />
    </fieldset>
  );
}

type ManagerKindFieldProps = Readonly<{
  formId: string;
  defaultValue: string | undefined;
  error: string | undefined;
  selectRef: RefObject<HTMLSelectElement | null>;
}>;

function ManagerKindField({
  formId,
  defaultValue,
  error,
  selectRef,
}: ManagerKindFieldProps): ReactNode {
  const id = `${formId}-download_manager_kind`;
  return (
    <div>
      <label htmlFor={id}>Download manager</label>
      <select
        ref={selectRef}
        id={id}
        name="download_manager_kind"
        required
        defaultValue={defaultValue ?? ""}
        aria-invalid={error !== undefined}
        aria-describedby={fieldErrorId(formId, "download_manager_kind")}
      >
        <option value="">Choose a download manager</option>
        {DOWNLOAD_MANAGER_KINDS.map((choice) => (
          <option key={choice.value} value={choice.value}>
            {choice.label}
          </option>
        ))}
      </select>
      <FieldError
        formId={formId}
        field="download_manager_kind"
        message={error}
      />
    </div>
  );
}
