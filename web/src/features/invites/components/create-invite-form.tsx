import {
  useEffect,
  useId,
  useRef,
  useState,
  type ReactNode,
  type SyntheticEvent,
} from "react";
import { FieldError, fieldErrorId } from "../../auth/components/field-error.js";
import type { CreateInviteRequest } from "../api/invites-schemas.js";
import {
  EXPIRY_CHOICES,
  firstInvalidField,
  readCreateInviteInput,
  type CreateInviteField,
  type CreateInviteFieldErrors,
} from "./create-invite-input.js";
import { describeCreateInviteError } from "./invite-errors.js";

export type ServerChoice = Readonly<{ id: string; name: string }>;

type CreateInviteFormProps = Readonly<{
  servers: readonly ServerChoice[];
  pending: boolean;
  serverError: unknown;
  now: () => Date;
  onSubmit: (input: CreateInviteRequest) => void;
}>;

// Uncontrolled on purpose: the browser keeps the typed values, so a rejected
// submit never loses input. The route remounts the form after a success.
export function CreateInviteForm({
  servers,
  pending,
  serverError,
  now,
  onSubmit,
}: CreateInviteFormProps): ReactNode {
  const formId = useId();
  const summaryRef = useRef<HTMLParagraphElement>(null);
  const serverRef = useRef<HTMLSelectElement>(null);
  const labelRef = useRef<HTMLInputElement>(null);
  const expiryRef = useRef<HTMLSelectElement>(null);
  const maxUsesRef = useRef<HTMLInputElement>(null);
  const [errors, setErrors] = useState<CreateInviteFieldErrors>({});
  const summary = describeCreateInviteError(serverError);

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
    const result = readCreateInviteInput(
      new FormData(event.currentTarget),
      now(),
    );
    if (result.errors !== undefined) {
      setErrors(result.errors);
      focusField(firstInvalidField(result.errors));
      return;
    }
    setErrors({});
    onSubmit(result.input);
  }

  function focusField(field: CreateInviteField | undefined): void {
    switch (field) {
      case "media_server_id":
        serverRef.current?.focus();
        break;
      case "label":
        labelRef.current?.focus();
        break;
      case "expiry":
        expiryRef.current?.focus();
        break;
      case "max_uses":
        maxUsesRef.current?.focus();
        break;
      case undefined:
        break;
    }
  }

  const summaryId = `${formId}-summary`;
  return (
    <form
      onSubmit={handleSubmit}
      aria-label="Create an invite"
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
        <label htmlFor={`${formId}-media_server_id`}>Media server</label>
        <select
          ref={serverRef}
          id={`${formId}-media_server_id`}
          name="media_server_id"
          required
          defaultValue={servers.length === 1 ? servers[0]?.id : ""}
          aria-invalid={errors.media_server_id !== undefined}
          aria-describedby={fieldErrorId(formId, "media_server_id")}
        >
          <option value="">Choose a server</option>
          {servers.map((server) => (
            <option key={server.id} value={server.id}>
              {server.name}
            </option>
          ))}
        </select>
        <FieldError
          formId={formId}
          field="media_server_id"
          message={errors.media_server_id}
        />
      </div>
      <div>
        <label htmlFor={`${formId}-label`}>Label</label>
        <input
          ref={labelRef}
          id={`${formId}-label`}
          name="label"
          type="text"
          autoComplete="off"
          required
          aria-invalid={errors.label !== undefined}
          aria-describedby={fieldErrorId(formId, "label")}
        />
        <FieldError formId={formId} field="label" message={errors.label} />
      </div>
      <div>
        <label htmlFor={`${formId}-expiry`}>Expires</label>
        <select
          ref={expiryRef}
          id={`${formId}-expiry`}
          name="expiry"
          defaultValue="week"
          aria-invalid={errors.expiry !== undefined}
          aria-describedby={fieldErrorId(formId, "expiry")}
        >
          {EXPIRY_CHOICES.map((choice) => (
            <option key={choice.value} value={choice.value}>
              {choice.label}
            </option>
          ))}
        </select>
        <FieldError formId={formId} field="expiry" message={errors.expiry} />
      </div>
      <div>
        <label htmlFor={`${formId}-max_uses`}>Use limit</label>
        <input
          ref={maxUsesRef}
          id={`${formId}-max_uses`}
          name="max_uses"
          type="text"
          inputMode="numeric"
          autoComplete="off"
          placeholder="No limit"
          aria-invalid={errors.max_uses !== undefined}
          aria-describedby={fieldErrorId(formId, "max_uses")}
        />
        <FieldError
          formId={formId}
          field="max_uses"
          message={errors.max_uses}
        />
      </div>
      <p>Every library on the server is granted.</p>
      <button type="submit" disabled={pending}>
        {pending ? "Creating…" : "Create invite"}
      </button>
      <p role="status">{pending ? "Creating the invite." : ""}</p>
    </form>
  );
}
