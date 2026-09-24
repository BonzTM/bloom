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
  downloadManagerKindSchema,
  type RegisterDownloadManagerRequest,
} from "../api/download-manager-schemas.js";
import {
  firstInvalidManagerField,
  readManagerInput,
  type ManagerField,
  type ManagerFieldErrors,
} from "./register-manager-input.js";
import { describeManagerError } from "./request-errors.js";

type RegisterDownloadManagerFormProps = Readonly<{
  pending: boolean;
  serverError: unknown;
  onSubmit: (input: RegisterDownloadManagerRequest) => void;
}>;

const KIND_LABELS = { radarr: "Radarr (movies)", sonarr: "Sonarr (series)" };

// Uncontrolled on purpose: the browser keeps the typed values, so a rejected
// submit never loses input. The route remounts the form after a success so
// the API key does not linger on screen.
export function RegisterDownloadManagerForm({
  pending,
  serverError,
  onSubmit,
}: RegisterDownloadManagerFormProps): ReactNode {
  const formId = useId();
  const summaryRef = useRef<HTMLParagraphElement>(null);
  const fieldRefs = useFieldRefs();
  const [errors, setErrors] = useState<ManagerFieldErrors>({});
  const summary = describeManagerError(serverError);

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
    const result = readManagerInput(new FormData(event.currentTarget));
    if (result.errors !== undefined) {
      setErrors(result.errors);
      const first = firstInvalidManagerField(result.errors);
      if (first !== undefined) {
        fieldRefs[first].current?.focus();
      }
      return;
    }
    setErrors({});
    onSubmit(result.input);
  }

  const summaryId = `${formId}-summary`;
  return (
    <form
      onSubmit={handleSubmit}
      aria-label="Register a download manager"
      noValidate
      aria-busy={pending}
      aria-describedby={summary === undefined ? undefined : summaryId}
    >
      {summary === undefined ? null : (
        <p id={summaryId} role="alert" tabIndex={-1} ref={summaryRef}>
          {summary}
        </p>
      )}
      <KindField
        formId={formId}
        error={errors.kind}
        selectRef={fieldRefs.kind}
      />
      <TextField
        formId={formId}
        name="name"
        label="Name"
        type="text"
        error={errors.name}
        inputRef={fieldRefs.name}
      />
      <TextField
        formId={formId}
        name="base_url"
        label="Address"
        type="url"
        placeholder="https://radarr.example"
        error={errors.base_url}
        inputRef={fieldRefs.base_url}
      />
      <TextField
        formId={formId}
        name="api_key"
        label="API key"
        type="password"
        error={errors.api_key}
        inputRef={fieldRefs.api_key}
      />
      <InsecureField
        formId={formId}
        error={errors.allow_insecure}
        inputRef={fieldRefs.allow_insecure}
      />
      <button type="submit" className="btn-primary" disabled={pending}>
        {pending ? "Checking the instance…" : "Register download manager"}
      </button>
      <p role="status">
        {pending ? "Contacting the instance to confirm the API key." : ""}
      </p>
    </form>
  );
}

type FieldRefs = Readonly<{
  kind: RefObject<HTMLSelectElement | null>;
  name: RefObject<HTMLInputElement | null>;
  base_url: RefObject<HTMLInputElement | null>;
  api_key: RefObject<HTMLInputElement | null>;
  allow_insecure: RefObject<HTMLInputElement | null>;
}>;

function useFieldRefs(): FieldRefs {
  return {
    kind: useRef<HTMLSelectElement>(null),
    name: useRef<HTMLInputElement>(null),
    base_url: useRef<HTMLInputElement>(null),
    api_key: useRef<HTMLInputElement>(null),
    allow_insecure: useRef<HTMLInputElement>(null),
  };
}

function InsecureField({
  formId,
  error,
  inputRef,
}: Readonly<{
  formId: string;
  error: string | undefined;
  inputRef: RefObject<HTMLInputElement | null>;
}>): ReactNode {
  const id = `${formId}-allow_insecure`;
  return (
    <div className="checkbox-field">
      <label htmlFor={id}>
        <input
          ref={inputRef}
          id={id}
          name="allow_insecure"
          type="checkbox"
          aria-invalid={error !== undefined}
          aria-describedby={fieldErrorId(formId, "allow_insecure")}
        />{" "}
        Allow plaintext HTTP. The API key is sent unencrypted on every request.
      </label>
      <FieldError formId={formId} field="allow_insecure" message={error} />
    </div>
  );
}

function KindField({
  formId,
  error,
  selectRef,
}: Readonly<{
  formId: string;
  error: string | undefined;
  selectRef: RefObject<HTMLSelectElement | null>;
}>): ReactNode {
  const id = `${formId}-kind`;
  return (
    <div>
      <label htmlFor={id}>Kind</label>
      <select
        ref={selectRef}
        id={id}
        name="kind"
        required
        defaultValue=""
        aria-invalid={error !== undefined}
        aria-describedby={fieldErrorId(formId, "kind")}
      >
        <option value="">Choose a kind</option>
        {downloadManagerKindSchema.options.map((kind) => (
          <option key={kind} value={kind}>
            {KIND_LABELS[kind]}
          </option>
        ))}
      </select>
      <FieldError formId={formId} field="kind" message={error} />
    </div>
  );
}

function TextField({
  formId,
  name,
  label,
  type,
  placeholder,
  error,
  inputRef,
}: Readonly<{
  formId: string;
  name: Exclude<ManagerField, "kind" | "allow_insecure">;
  label: string;
  type: "text" | "url" | "password";
  placeholder?: string;
  error: string | undefined;
  inputRef: RefObject<HTMLInputElement | null>;
}>): ReactNode {
  const id = `${formId}-${name}`;
  return (
    <div>
      <label htmlFor={id}>{label}</label>
      <input
        ref={inputRef}
        id={id}
        name={name}
        type={type}
        placeholder={placeholder}
        autoComplete="off"
        spellCheck={false}
        required
        aria-invalid={error !== undefined}
        aria-describedby={fieldErrorId(formId, name)}
      />
      <FieldError formId={formId} field={name} message={error} />
    </div>
  );
}
