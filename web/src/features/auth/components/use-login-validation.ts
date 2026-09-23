import { useEffect, useRef, useState, type RefObject } from "react";
import { loginInputSchema, type LoginInput } from "../api/auth-schemas.js";

type LoginFieldErrors = Readonly<Partial<Record<keyof LoginInput, string>>>;

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
  const pendingFocus = useRef<keyof LoginInput | null>(null);
  const username = useRef<HTMLInputElement>(null);
  const password = useRef<HTMLInputElement>(null);
  const refs: FieldRefs = { username, password };

  // Focus moves after React commits the error text, so the field's accessible
  // description is already in place when assistive technology reads it. The
  // errors state is the trigger; the target rides in a ref so the effect sets
  // no state of its own.
  useEffect(() => {
    const target = pendingFocus.current;
    if (target === null) {
      return;
    }
    pendingFocus.current = null;
    (target === "username" ? username : password).current?.focus();
  }, [errors]);

  function validate(form: HTMLFormElement): LoginInput | undefined {
    const parsed = loginInputSchema.safeParse(
      Object.fromEntries(new FormData(form)),
    );
    if (parsed.success) {
      setErrors({});
      return parsed.data;
    }
    const next = collectFieldErrors(parsed.error.issues);
    pendingFocus.current = firstInvalid(next);
    setErrors(next);
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

function firstInvalid(errors: LoginFieldErrors): keyof LoginInput | null {
  return FIELD_ORDER.find((field) => errors[field] !== undefined) ?? null;
}
