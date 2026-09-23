import {
  useEffect,
  useId,
  useRef,
  type ReactNode,
  type SyntheticEvent,
} from "react";
import type { LoginInput } from "../api/auth-schemas.js";
import { CredentialField } from "./credential-field.js";
import { describeLoginError } from "./login-errors.js";
import { useLoginValidation } from "./use-login-validation.js";

type LoginFormProps = Readonly<{
  pending: boolean;
  serverError: unknown;
  onSubmit: (input: LoginInput) => void;
}>;

export function LoginForm({
  pending,
  serverError,
  onSubmit,
}: LoginFormProps): ReactNode {
  const formId = useId();
  const summaryRef = useRef<HTMLParagraphElement>(null);
  const { errors, refs, validate } = useLoginValidation();
  const summary = describeLoginError(serverError);

  // A rejected sign-in moves focus to the explanation. The typed values stay
  // in the uncontrolled inputs so a retry costs nothing.
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
    const input = validate(event.currentTarget);
    if (input !== undefined) {
      onSubmit(input);
    }
  }

  const summaryId = `${formId}-summary`;
  return (
    <form
      onSubmit={handleSubmit}
      aria-label="Sign in"
      noValidate
      aria-busy={pending}
      aria-describedby={summary === undefined ? undefined : summaryId}
    >
      {summary === undefined ? null : (
        <p id={summaryId} role="alert" tabIndex={-1} ref={summaryRef}>
          {summary}
        </p>
      )}
      <CredentialField
        formId={formId}
        name="username"
        label="Username"
        type="text"
        autoComplete="username"
        error={errors.username}
        inputRef={refs.username}
      />
      <CredentialField
        formId={formId}
        name="password"
        label="Password"
        type="password"
        autoComplete="current-password"
        error={errors.password}
        inputRef={refs.password}
      />
      <button type="submit" className="btn-primary" disabled={pending}>
        {pending ? "Signing in…" : "Sign in"}
      </button>
      <p role="status">{pending ? "Signing in, please wait." : ""}</p>
    </form>
  );
}
