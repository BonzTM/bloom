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
import type { RegisterMediaServerInput } from "../api/media-servers-schemas.js";
import { describeRegisterError } from "./media-server-errors.js";
import {
  readRegisterInput,
  type RegisterField,
  type RegisterFieldErrors,
} from "./register-input.js";

type RegisterMediaServerFormProps = Readonly<{
  pending: boolean;
  serverError: unknown;
  onSubmit: (input: RegisterMediaServerInput) => void;
}>;

// Uncontrolled on purpose: the browser keeps the typed values, so a rejected
// submit never loses input. The route remounts the form after a success so
// the API key does not linger on screen.
export function RegisterMediaServerForm({
  pending,
  serverError,
  onSubmit,
}: RegisterMediaServerFormProps): ReactNode {
  const formId = useId();
  const summaryRef = useRef<HTMLParagraphElement>(null);
  const fieldRefs = useFieldRefs();
  const [errors, setErrors] = useState<RegisterFieldErrors>({});
  const summary = describeRegisterError(serverError);

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
    const result = readRegisterInput(new FormData(event.currentTarget));
    if (result.errors !== undefined) {
      setErrors(result.errors);
      focusFirstInvalid(result.errors, fieldRefs);
      return;
    }
    setErrors({});
    onSubmit(result.input);
  }

  const summaryId = `${formId}-summary`;
  return (
    <form
      onSubmit={handleSubmit}
      aria-label="Register a media server"
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
        inputRef={fieldRefs.name}
        label="Name"
        type="text"
        error={errors.name}
      />
      <TextField
        formId={formId}
        name="base_url"
        inputRef={fieldRefs.base_url}
        label="Address"
        type="url"
        placeholder="https://jellyfin.example"
        error={errors.base_url}
      />
      <TextField
        formId={formId}
        name="api_key"
        inputRef={fieldRefs.api_key}
        label="API key"
        type="password"
        error={errors.api_key}
      />
      <InsecureField
        formId={formId}
        error={errors.allow_insecure}
        inputRef={fieldRefs.allow_insecure}
      />
      <button type="submit" disabled={pending}>
        {pending ? "Checking the server…" : "Register server"}
      </button>
      <p role="status">
        {pending ? "Contacting the server to confirm the API key." : ""}
      </p>
    </form>
  );
}

type FieldRefs = Readonly<
  Record<RegisterField, RefObject<HTMLInputElement | null>>
>;

const FIELD_ORDER: readonly RegisterField[] = [
  "name",
  "base_url",
  "api_key",
  "allow_insecure",
];

function useFieldRefs(): FieldRefs {
  return {
    name: useRef<HTMLInputElement>(null),
    base_url: useRef<HTMLInputElement>(null),
    api_key: useRef<HTMLInputElement>(null),
    allow_insecure: useRef<HTMLInputElement>(null),
  };
}

// A rejected submit moves focus to the first control that needs attention,
// in the order the fields appear on screen.
function focusFirstInvalid(errors: RegisterFieldErrors, refs: FieldRefs): void {
  const first = FIELD_ORDER.find((field) => errors[field] !== undefined);
  if (first !== undefined) {
    refs[first].current?.focus();
  }
}

type TextFieldProps = Readonly<{
  formId: string;
  name: Exclude<RegisterField, "allow_insecure">;
  label: string;
  type: "text" | "url" | "password";
  placeholder?: string;
  error: string | undefined;
  inputRef: RefObject<HTMLInputElement | null>;
}>;

function TextField({
  formId,
  name,
  label,
  type,
  placeholder,
  error,
  inputRef,
}: TextFieldProps): ReactNode {
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
