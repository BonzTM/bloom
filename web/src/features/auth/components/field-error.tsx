import type { ReactNode } from "react";

type FieldErrorProps = Readonly<{
  formId: string;
  field: string;
  message: string | undefined;
}>;

export function fieldErrorId(formId: string, field: string): string {
  return `${formId}-${field}-error`;
}

// Renders a field's validation message next to its control. The element is
// always present so `aria-describedby` on the input stays valid; it is empty
// when there is nothing to say.
export function FieldError({
  formId,
  field,
  message,
}: FieldErrorProps): ReactNode {
  return (
    <p id={fieldErrorId(formId, field)} className="field-error">
      {message}
    </p>
  );
}
