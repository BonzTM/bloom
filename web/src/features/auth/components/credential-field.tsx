import type { ReactNode, RefObject } from "react";
import { FieldError, fieldErrorId } from "./field-error.js";

type CredentialFieldProps = Readonly<{
  formId: string;
  name: "username" | "password";
  label: string;
  type: "text" | "password";
  autoComplete: "username" | "current-password";
  error: string | undefined;
  inputRef: RefObject<HTMLInputElement | null>;
}>;

// One labelled input with its validation message. Uncontrolled on purpose: the
// browser keeps the typed value, so a rejected submit never loses input.
export function CredentialField({
  formId,
  name,
  label,
  type,
  autoComplete,
  error,
  inputRef,
}: CredentialFieldProps): ReactNode {
  const id = `${formId}-${name}`;
  return (
    <div>
      <label htmlFor={id}>{label}</label>
      <input
        ref={inputRef}
        id={id}
        name={name}
        type={type}
        autoComplete={autoComplete}
        required
        aria-invalid={error !== undefined}
        aria-describedby={fieldErrorId(formId, name)}
      />
      <FieldError formId={formId} field={name} message={error} />
    </div>
  );
}
