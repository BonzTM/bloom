import {
  useEffect,
  useId,
  useRef,
  useState,
  type ReactNode,
  type RefObject,
  type SyntheticEvent,
} from "react";
import { Link } from "react-router-dom";
import { AsyncStatus } from "../../../components/async-status.js";
import { FieldError, fieldErrorId } from "../../auth/components/field-error.js";
import type {
  DownloadManager,
  DownloadManagerOption,
} from "../api/download-manager-schemas.js";
import type {
  RequestProfile,
  RequestProfileInput,
} from "../api/requests-schemas.js";
import { useDownloadManagerOptions } from "../hooks/requests-queries.js";
import { managerKindLabel } from "./download-managers-table.js";
import {
  firstInvalidProfileField,
  readProfileInput,
  type ProfileField,
  type ProfileFieldErrors,
} from "./profile-input.js";
import { describeProfileError } from "./request-errors.js";

type RequestProfileFormProps = Readonly<{
  accountId: string;
  managers: readonly DownloadManager[];
  // The profile being edited; absent when creating a new one.
  profile: RequestProfile | undefined;
  pending: boolean;
  serverError: unknown;
  onSubmit: (input: RequestProfileInput) => void;
  onCancel: (() => void) | undefined;
}>;

// A profile names a registered instance; its quality profile, root folder,
// and tags are chosen from what that instance offers. Text fields stay
// uncontrolled so a rejected submit never loses input; the instance choice
// is controlled because it decides which options are shown.
export function RequestProfileForm({
  accountId,
  managers,
  profile,
  pending,
  serverError,
  onSubmit,
  onCancel,
}: RequestProfileFormProps): ReactNode {
  const formId = useId();
  const summaryRef = useRef<HTMLParagraphElement>(null);
  const fieldRefs = useFieldRefs();
  const [errors, setErrors] = useState<ProfileFieldErrors>({});
  const [managerId, setManagerId] = useState(
    () => managerFor(profile, managers)?.id ?? "",
  );
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
    const result = readProfileInput(
      new FormData(event.currentTarget),
      managers,
    );
    if (result.errors !== undefined) {
      setErrors(result.errors);
      focusFirstInvalid(result.errors, fieldRefs);
      return;
    }
    setErrors({});
    onSubmit(result.input);
  }

  if (managers.length === 0) {
    return (
      <p>
        No download manager is registered yet, so no profile can be created.{" "}
        <Link to="/admin/download-managers">Register one first.</Link>
      </p>
    );
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
      <NameField
        formId={formId}
        defaultValue={profile?.name}
        error={errors.name}
        inputRef={fieldRefs.name}
      />
      <ManagerSelect
        formId={formId}
        managers={managers}
        value={managerId}
        onChange={setManagerId}
        error={errors.download_manager}
        selectRef={fieldRefs.download_manager}
      />
      {managerId === "" ? (
        <p className="field-hint">
          Choose an instance to pick its quality profile and root folder.
        </p>
      ) : (
        <OptionFields
          key={managerId}
          formId={formId}
          accountId={accountId}
          managerId={managerId}
          profile={profile}
          errors={errors}
          qualityRef={fieldRefs.quality_profile}
          rootRef={fieldRefs.root_folder}
          tagsRef={fieldRefs.tags}
        />
      )}
      <div className="form-actions">
        <button type="submit" className="btn-primary" disabled={pending}>
          {pending ? "Saving…" : editing ? "Save profile" : "Create profile"}
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

function NameField({
  formId,
  defaultValue,
  error,
  inputRef,
}: Readonly<{
  formId: string;
  defaultValue: string | undefined;
  error: string | undefined;
  inputRef: RefObject<HTMLInputElement | null>;
}>): ReactNode {
  const id = `${formId}-name`;
  return (
    <div>
      <label htmlFor={id}>Name</label>
      <input
        ref={inputRef}
        id={id}
        name="name"
        type="text"
        autoComplete="off"
        spellCheck={false}
        required
        defaultValue={defaultValue}
        aria-invalid={error !== undefined}
        aria-describedby={fieldErrorId(formId, "name")}
      />
      <FieldError formId={formId} field="name" message={error} />
    </div>
  );
}

function ManagerSelect({
  formId,
  managers,
  value,
  onChange,
  error,
  selectRef,
}: Readonly<{
  formId: string;
  managers: readonly DownloadManager[];
  value: string;
  onChange: (value: string) => void;
  error: string | undefined;
  selectRef: RefObject<HTMLSelectElement | null>;
}>): ReactNode {
  const id = `${formId}-download_manager`;
  return (
    <div>
      <label htmlFor={id}>Download manager</label>
      <select
        ref={selectRef}
        id={id}
        name="download_manager"
        required
        value={value}
        onChange={(event) => {
          onChange(event.target.value);
        }}
        aria-invalid={error !== undefined}
        aria-describedby={fieldErrorId(formId, "download_manager")}
      >
        <option value="">Choose an instance</option>
        {managers.map((manager) => (
          <option key={manager.id} value={manager.id}>
            {manager.name} ({managerKindLabel(manager.kind)})
          </option>
        ))}
      </select>
      <FieldError formId={formId} field="download_manager" message={error} />
    </div>
  );
}

function managerFor(
  profile: RequestProfile | undefined,
  managers: readonly DownloadManager[],
): DownloadManager | undefined {
  if (profile === undefined) {
    return undefined;
  }
  return managers.find(
    (manager) =>
      manager.name === profile.download_manager_instance &&
      manager.kind === profile.download_manager_kind,
  );
}

type FieldRefs = Readonly<{
  name: RefObject<HTMLInputElement | null>;
  download_manager: RefObject<HTMLSelectElement | null>;
  quality_profile: RefObject<HTMLSelectElement | null>;
  root_folder: RefObject<HTMLSelectElement | null>;
  tags: RefObject<HTMLInputElement | null>;
}>;

function useFieldRefs(): FieldRefs {
  return {
    name: useRef<HTMLInputElement>(null),
    download_manager: useRef<HTMLSelectElement>(null),
    quality_profile: useRef<HTMLSelectElement>(null),
    root_folder: useRef<HTMLSelectElement>(null),
    tags: useRef<HTMLInputElement>(null),
  };
}

function focusFirstInvalid(
  errors: ProfileFieldErrors,
  fieldRefs: FieldRefs,
): void {
  const first: ProfileField | undefined = firstInvalidProfileField(errors);
  if (first !== undefined) {
    fieldRefs[first].current?.focus();
  }
}

type OptionFieldsProps = Readonly<{
  formId: string;
  accountId: string;
  managerId: string;
  profile: RequestProfile | undefined;
  errors: ProfileFieldErrors;
  qualityRef: RefObject<HTMLSelectElement | null>;
  rootRef: RefObject<HTMLSelectElement | null>;
  tagsRef: RefObject<HTMLInputElement | null>;
}>;

// The choices the chosen instance offers. Mounted per instance, so a
// change of instance starts the selects over.
function OptionFields({
  formId,
  accountId,
  managerId,
  profile,
  errors,
  qualityRef,
  rootRef,
  tagsRef,
}: OptionFieldsProps): ReactNode {
  const options = useDownloadManagerOptions(accountId, managerId);
  if (options.status === "pending") {
    return <AsyncStatus>Loading the instance's options…</AsyncStatus>;
  }
  if (options.status === "error") {
    return (
      <>
        <AsyncStatus kind="alert">
          The instance's options could not be loaded, so the profile cannot be
          filled in yet.
        </AsyncStatus>
        <button
          type="button"
          onClick={() => {
            void options.refetch();
          }}
        >
          Retry
        </button>
      </>
    );
  }
  return (
    <>
      <OptionSelect
        formId={formId}
        name="quality_profile"
        label="Quality profile"
        choices={options.data.quality_profiles}
        defaultValue={profile?.quality_profile}
        error={errors.quality_profile}
        selectRef={qualityRef}
      />
      <OptionSelect
        formId={formId}
        name="root_folder"
        label="Root folder"
        choices={options.data.root_folders}
        defaultValue={profile?.root_folder}
        error={errors.root_folder}
        selectRef={rootRef}
      />
      <TagsField
        formId={formId}
        choices={options.data.tags}
        defaultTags={profile?.tags ?? []}
        error={errors.tags}
        inputRef={tagsRef}
      />
    </>
  );
}

function OptionSelect({
  formId,
  name,
  label,
  choices,
  defaultValue,
  error,
  selectRef,
}: Readonly<{
  formId: string;
  name: "quality_profile" | "root_folder";
  label: string;
  choices: readonly DownloadManagerOption[];
  defaultValue: string | undefined;
  error: string | undefined;
  selectRef: RefObject<HTMLSelectElement | null>;
}>): ReactNode {
  const id = `${formId}-${name}`;
  return (
    <div>
      <label htmlFor={id}>{label}</label>
      <select
        ref={selectRef}
        id={id}
        name={name}
        required
        defaultValue={
          defaultValue ?? (choices.length === 1 ? choices[0]?.name : "")
        }
        aria-invalid={error !== undefined}
        aria-describedby={fieldErrorId(formId, name)}
      >
        {choices.length === 1 ? null : <option value="">Choose one</option>}
        {choices.map((choice) => (
          <option key={choice.id} value={choice.name}>
            {choice.name}
          </option>
        ))}
      </select>
      <FieldError formId={formId} field={name} message={error} />
    </div>
  );
}

function TagsField({
  formId,
  choices,
  defaultTags,
  error,
  inputRef,
}: Readonly<{
  formId: string;
  choices: readonly DownloadManagerOption[];
  defaultTags: readonly string[];
  error: string | undefined;
  inputRef: RefObject<HTMLInputElement | null>;
}>): ReactNode {
  if (choices.length === 0) {
    return <p className="field-hint">This instance has no tags.</p>;
  }
  return (
    <fieldset
      className="checkbox-group"
      aria-invalid={error !== undefined}
      aria-describedby={fieldErrorId(formId, "tags")}
    >
      <legend>Tags</legend>
      {choices.map((choice, index) => {
        const id = `${formId}-tag-${choice.id}`;
        return (
          <div key={choice.id} className="checkbox-field">
            <label htmlFor={id}>
              <input
                ref={index === 0 ? inputRef : undefined}
                id={id}
                name="tags"
                type="checkbox"
                value={choice.name}
                defaultChecked={defaultTags.includes(choice.name)}
              />{" "}
              {choice.name}
            </label>
          </div>
        );
      })}
      <FieldError formId={formId} field="tags" message={error} />
    </fieldset>
  );
}
