import {
  useEffect,
  useId,
  useRef,
  useState,
  type ReactNode,
  type SyntheticEvent,
} from "react";
import { FieldError, fieldErrorId } from "../../auth/components/field-error.js";
import type {
  AcceptInviteRequest,
  PublicInvite,
} from "../api/invites-schemas.js";
import {
  firstInvalidAcceptField,
  readAcceptInviteInput,
  type AcceptInviteFieldErrors,
} from "./accept-invite-input.js";
import { describeAcceptError } from "./invite-errors.js";

type AcceptInviteFormProps = Readonly<{
  invite: PublicInvite;
  pending: boolean;
  serverError: unknown;
  onSubmit: (input: AcceptInviteRequest) => void;
}>;

// Uncontrolled on purpose: the browser keeps the typed values, so a rejected
// submit never loses input. The password stays in its field after a
// rejection so a retry costs nothing; the route unmounts the form on success.
export function AcceptInviteForm({
  invite,
  pending,
  serverError,
  onSubmit,
}: AcceptInviteFormProps): ReactNode {
  const formId = useId();
  const summaryRef = useRef<HTMLParagraphElement>(null);
  const usernameRef = useRef<HTMLInputElement>(null);
  const passwordRef = useRef<HTMLInputElement>(null);
  const [errors, setErrors] = useState<AcceptInviteFieldErrors>({});
  const summary = describeAcceptError(serverError);

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
    const result = readAcceptInviteInput(new FormData(event.currentTarget));
    if (result.errors !== undefined) {
      setErrors(result.errors);
      const first = firstInvalidAcceptField(result.errors);
      (first === "password" ? passwordRef : usernameRef).current?.focus();
      return;
    }
    setErrors({});
    onSubmit(result.input);
  }

  const summaryId = `${formId}-summary`;
  const usernameRuleId = `${formId}-username-rule`;
  const passwordRuleId = `${formId}-password-rule`;
  return (
    <form
      onSubmit={handleSubmit}
      aria-label={`Create your account on ${invite.media_server_name}`}
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
        <label htmlFor={`${formId}-username`}>Username</label>
        <p id={usernameRuleId} className="field-hint">
          {invite.username_rule}
        </p>
        <input
          ref={usernameRef}
          id={`${formId}-username`}
          name="username"
          type="text"
          autoComplete="username"
          spellCheck={false}
          required
          aria-invalid={errors.username !== undefined}
          aria-describedby={`${usernameRuleId} ${fieldErrorId(formId, "username")}`}
        />
        <FieldError
          formId={formId}
          field="username"
          message={errors.username}
        />
      </div>
      <div>
        <label htmlFor={`${formId}-password`}>Password</label>
        <p id={passwordRuleId} className="field-hint">
          {invite.password_rule}
        </p>
        <input
          ref={passwordRef}
          id={`${formId}-password`}
          name="password"
          type="password"
          autoComplete="new-password"
          required
          aria-invalid={errors.password !== undefined}
          aria-describedby={`${passwordRuleId} ${fieldErrorId(formId, "password")}`}
        />
        <FieldError
          formId={formId}
          field="password"
          message={errors.password}
        />
      </div>
      <button type="submit" className="btn-primary" disabled={pending}>
        {pending ? "Creating your account…" : "Create my account"}
      </button>
      <p role="status">
        {pending ? "Creating your account on the media server." : ""}
      </p>
    </form>
  );
}
