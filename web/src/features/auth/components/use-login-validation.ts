import { useRef, useState, type RefObject } from "react";
import { loginInputSchema, type LoginInput } from "../api/auth-schemas.js";

export type LoginFieldErrors = Readonly<
  Partial<Record<keyof LoginInput, string>>
>;

type FieldRefs = Readonly<
  Record<keyof LoginInput, RefObject<HTMLInputElement | null>>
>;

type LoginValidation = Readonly<{
  errors: LoginFieldErrors;
  refs: FieldRefs;
  validate: (form: HTMLFormElement) => LoginInput | undefined;
}>;

const FIELD_ORDER = ["username", "password"] as const;

// Client-side validation and focus policy for the sign-in form, kept apart
// from rendering so the decisions are testable without a full DOM tree.
export function useLoginValidation(): LoginValidation {
  const [errors, setErrors] = useState<LoginFieldErrors>({});
  const username = useRef<HTMLInputElement>(null);
  const password = useRef<HTMLInputElement>(null);
  const refs: FieldRefs = { username, password };

  function validate(form: HTMLFormElement): LoginInput | undefined {
    const parsed = loginInputSchema.safeParse(
      Object.fromEntries(new FormData(form)),
    );
    if (parsed.success) {
      setErrors({});
      return parsed.data;
    }
    const next = collectFieldErrors(parsed.error.issues);
    setErrors(next);
    focusFirstInvalid(next, refs);
    return undefined;
  }

  return { errors, refs, validate };
}

function collectFieldErrors(
  issues: readonly { path: readonly PropertyKey[]; message: string }[],
): LoginFieldErrors {
  const errors: Partial<Record<keyof LoginInput, string>> = {};
  for (const issue of issues) {
    const field = issue.path[0];
    if (isLoginField(field) && errors[field] === undefined) {
      errors[field] = issue.message;
    }
  }
  return errors;
}

function isLoginField(
  value: PropertyKey | undefined,
): value is keyof LoginInput {
  return value === "username" || value === "password";
}

function focusFirstInvalid(errors: LoginFieldErrors, refs: FieldRefs): void {
  const first = FIELD_ORDER.find((field) => errors[field] !== undefined);
  if (first !== undefined) {
    refs[first].current?.focus();
  }
}
