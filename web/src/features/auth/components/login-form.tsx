import {
  useId,
  useRef,
  useState,
  type SyntheticEvent,
  type ReactNode,
  type RefObject,
} from "react";
import { ApiError } from "../../../lib/api/errors.js";
import { loginInputSchema, type LoginInput } from "../api/auth-schemas.js";
import { FieldError, fieldErrorId } from "./field-error.js";
import { describeLoginError } from "./login-errors.js";

type LoginFormProps = Readonly<{
  pending: boolean;
  serverError: unknown;
  onSubmit: (input: LoginInput) => void;
}>;

type FieldErrors = Readonly<Partial<Record<keyof LoginInput, string>>>;

export function LoginForm({
  pending,
  serverError,
  onSubmit,
}: LoginFormProps): ReactNode {
  const formId = useId();
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});
  const usernameRef = useRef<HTMLInputElement>(null);
  const passwordRef = useRef<HTMLInputElement>(null);

  function handleSubmit(event: SyntheticEvent<HTMLFormElement>): void {
    event.preventDefault();
    if (pending) {
      return;
    }
    const parsed = loginInputSchema.safeParse(
      Object.fromEntries(new FormData(event.currentTarget)),
    );
    if (!parsed.success) {
      const errors = collectFieldErrors(parsed.error.issues);
      setFieldErrors(errors);
      focusFirstInvalid(errors, {
        username: usernameRef,
        password: passwordRef,
      });
      return;
    }
    setFieldErrors({});
    onSubmit(parsed.data);
  }

  const summary = describeLoginError(serverError);
  return (
    <form
      onSubmit={handleSubmit}
      noValidate
      aria-describedby={`${formId}-summary`}
    >
      {summary === undefined ? null : (
        <p id={`${formId}-summary`} role="alert">
          {summary}
        </p>
      )}
      <div>
        <label htmlFor={`${formId}-username`}>Username</label>
        <input
          ref={usernameRef}
          id={`${formId}-username`}
          name="username"
          type="text"
          autoComplete="username"
          required
          aria-invalid={fieldErrors.username !== undefined}
          aria-describedby={fieldErrorId(formId, "username")}
        />
        <FieldError
          formId={formId}
          field="username"
          message={fieldErrors.username}
        />
      </div>
      <div>
        <label htmlFor={`${formId}-password`}>Password</label>
        <input
          ref={passwordRef}
          id={`${formId}-password`}
          name="password"
          type="password"
          autoComplete="current-password"
          required
          aria-invalid={fieldErrors.password !== undefined}
          aria-describedby={fieldErrorId(formId, "password")}
        />
        <FieldError
          formId={formId}
          field="password"
          message={fieldErrors.password}
        />
      </div>
      <button type="submit" disabled={pending} aria-disabled={pending}>
        {pending ? "Signing in…" : "Sign in"}
      </button>
    </form>
  );
}

function collectFieldErrors(
  issues: readonly { path: readonly PropertyKey[]; message: string }[],
): FieldErrors {
  const errors: Partial<Record<keyof LoginInput, string>> = {};
  for (const issue of issues) {
    const field = issue.path[0];
    if (
      (field === "username" || field === "password") &&
      errors[field] === undefined
    ) {
      errors[field] = issue.message;
    }
  }
  return errors;
}

function focusFirstInvalid(
  errors: FieldErrors,
  refs: Readonly<Record<keyof LoginInput, RefObject<HTMLInputElement | null>>>,
): void {
  const first = (["username", "password"] as const).find(
    (field) => errors[field] !== undefined,
  );
  if (first !== undefined) {
    refs[first].current?.focus();
  }
}

export function isRetryableLoginError(error: unknown): boolean {
  return error instanceof ApiError && error.status === 429;
}
