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
  authModeSchema,
  notificationEventTypeSchema,
  notificationKindSchema,
  tlsModeSchema,
  type ChannelRequest,
  type NotificationChannel,
  type NotificationKind,
} from "../api/notification-schemas.js";
import {
  firstInvalidChannelField,
  readChannelInput,
  type ChannelField,
  type ChannelFieldErrors,
} from "./channel-input.js";
import { describeChannelError } from "./notification-errors.js";
import { eventLabel, kindLabel } from "./notification-format.js";

type ChannelFormProps = Readonly<{
  // The channel being edited; absent when creating a new one.
  channel: NotificationChannel | undefined;
  pending: boolean;
  serverError: unknown;
  onSubmit: (input: ChannelRequest) => void;
  onCancel: (() => void) | undefined;
}>;

type Refs = Readonly<Record<ChannelField, RefObject<HTMLElement | null>>>;

// Uncontrolled except for the kind, which decides which fields show. A
// rejected submit never loses input; the route remounts the form after a
// success so no secret lingers on screen.
export function ChannelForm({
  channel,
  pending,
  serverError,
  onSubmit,
  onCancel,
}: ChannelFormProps): ReactNode {
  const formId = useId();
  const summaryRef = useRef<HTMLParagraphElement>(null);
  const fieldRefs = useFieldRefs();
  const [errors, setErrors] = useState<ChannelFieldErrors>({});
  const [kind, setKind] = useState<NotificationKind | "">(channel?.kind ?? "");
  const summary = describeChannelError(serverError);
  const editing = channel !== undefined;

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
    const result = readChannelInput(new FormData(event.currentTarget), channel);
    if (result.errors !== undefined) {
      setErrors(result.errors);
      const first = firstInvalidChannelField(result.errors);
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
      aria-label={
        editing ? `Edit channel ${channel.name}` : "Register a channel"
      }
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
        value={kind}
        onChange={setKind}
        error={errors.kind}
        fieldRef={fieldRefs.kind}
      />
      <TextField
        formId={formId}
        name="name"
        label="Name"
        defaultValue={channel?.name}
        error={errors.name}
        fieldRef={fieldRefs.name}
      />
      <CheckboxField
        formId={formId}
        name="enabled"
        label="Enabled. Subscribed events are delivered while this is on."
        defaultChecked={channel?.enabled ?? true}
      />
      <SubscriptionsField
        formId={formId}
        defaultValue={
          channel?.subscriptions ?? ["approved", "available", "failed"]
        }
        error={errors.subscriptions}
        fieldRef={fieldRefs.subscriptions}
      />
      {kind === "webhook" ? (
        <WebhookFields
          formId={formId}
          channel={channel}
          errors={errors}
          fieldRefs={fieldRefs}
        />
      ) : kind === "discord" ? (
        <DiscordFields
          formId={formId}
          channel={channel}
          errors={errors}
          fieldRefs={fieldRefs}
        />
      ) : kind === "email" ? (
        <EmailFields
          formId={formId}
          channel={channel}
          errors={errors}
          fieldRefs={fieldRefs}
        />
      ) : null}
      <TemplateFields
        formId={formId}
        channel={channel}
        errors={errors}
        fieldRefs={fieldRefs}
      />
      <div className="form-actions">
        <button type="submit" className="btn-primary" disabled={pending}>
          {pending
            ? "Checking the channel…"
            : editing
              ? "Save channel"
              : "Register channel"}
        </button>
        {onCancel === undefined ? null : (
          <button type="button" className="btn-ghost" onClick={onCancel}>
            Cancel editing
          </button>
        )}
      </div>
      <p role="status">
        {pending ? "Contacting the channel to confirm it works." : ""}
      </p>
    </form>
  );
}

function useFieldRefs(): Refs {
  return {
    kind: useRef<HTMLElement>(null),
    name: useRef<HTMLElement>(null),
    subscriptions: useRef<HTMLElement>(null),
    subject_template: useRef<HTMLElement>(null),
    body_template: useRef<HTMLElement>(null),
    url: useRef<HTMLElement>(null),
    shared_secret: useRef<HTMLElement>(null),
    webhook_url: useRef<HTMLElement>(null),
    smtp_host: useRef<HTMLElement>(null),
    smtp_port: useRef<HTMLElement>(null),
    username: useRef<HTMLElement>(null),
    password: useRef<HTMLElement>(null),
    from_address: useRef<HTMLElement>(null),
    recipients: useRef<HTMLElement>(null),
    allow_insecure: useRef<HTMLElement>(null),
  };
}

type PartProps = Readonly<{
  formId: string;
  channel: NotificationChannel | undefined;
  errors: ChannelFieldErrors;
  fieldRefs: Refs;
}>;

function KindField({
  formId,
  value,
  onChange,
  error,
  fieldRef,
}: Readonly<{
  formId: string;
  value: NotificationKind | "";
  onChange: (kind: NotificationKind | "") => void;
  error: string | undefined;
  fieldRef: RefObject<HTMLElement | null>;
}>): ReactNode {
  const id = `${formId}-kind`;
  return (
    <div>
      <label htmlFor={id}>Kind</label>
      <select
        ref={fieldRef as RefObject<HTMLSelectElement | null>}
        id={id}
        name="kind"
        required
        value={value}
        onChange={(event) => {
          const parsed = notificationKindSchema.safeParse(event.target.value);
          onChange(parsed.success ? parsed.data : "");
        }}
        aria-invalid={error !== undefined}
        aria-describedby={fieldErrorId(formId, "kind")}
      >
        <option value="">Choose a kind</option>
        {notificationKindSchema.options.map((kind) => (
          <option key={kind} value={kind}>
            {kindLabel(kind)}
          </option>
        ))}
      </select>
      <FieldError formId={formId} field="kind" message={error} />
    </div>
  );
}

function SubscriptionsField({
  formId,
  defaultValue,
  error,
  fieldRef,
}: Readonly<{
  formId: string;
  defaultValue: readonly string[];
  error: string | undefined;
  fieldRef: RefObject<HTMLElement | null>;
}>): ReactNode {
  return (
    <fieldset
      className="checkbox-group"
      aria-invalid={error !== undefined}
      aria-describedby={fieldErrorId(formId, "subscriptions")}
    >
      <legend>Events</legend>
      {notificationEventTypeSchema.options.map((event, index) => {
        const id = `${formId}-event-${event}`;
        return (
          <div key={event} className="checkbox-field">
            <label htmlFor={id}>
              <input
                ref={
                  index === 0
                    ? (fieldRef as RefObject<HTMLInputElement | null>)
                    : undefined
                }
                id={id}
                name="subscriptions"
                type="checkbox"
                value={event}
                defaultChecked={defaultValue.includes(event)}
              />{" "}
              {eventLabel(event)}
            </label>
          </div>
        );
      })}
      <FieldError formId={formId} field="subscriptions" message={error} />
    </fieldset>
  );
}

function WebhookFields({
  formId,
  channel,
  errors,
  fieldRefs,
}: PartProps): ReactNode {
  const stored = channel?.kind === "webhook" ? channel.credentials : undefined;
  return (
    <>
      <TextField
        formId={formId}
        name="url"
        label="Webhook address"
        type="url"
        placeholder="https://hooks.example/bloom"
        hint={stored?.url_set ? "Stored. Leave blank to keep it." : undefined}
        required={stored?.url_set !== true}
        error={errors.url}
        fieldRef={fieldRefs.url}
      />
      <TextField
        formId={formId}
        name="shared_secret"
        label="Shared secret"
        type="password"
        hint={
          stored?.shared_secret_set
            ? "Stored. Leave blank to keep it."
            : "Signs every payload with HMAC-SHA256 in X-Bloom-Signature."
        }
        required={stored?.shared_secret_set !== true}
        error={errors.shared_secret}
        fieldRef={fieldRefs.shared_secret}
      />
      <TransportFields
        formId={formId}
        channel={channel}
        errors={errors}
        fieldRefs={fieldRefs}
      />
    </>
  );
}

function DiscordFields({
  formId,
  channel,
  errors,
  fieldRefs,
}: PartProps): ReactNode {
  const stored = channel?.kind === "discord" ? channel.credentials : undefined;
  return (
    <>
      <TextField
        formId={formId}
        name="webhook_url"
        label="Discord webhook URL"
        type="password"
        hint={
          stored?.url_set
            ? "Stored. Leave blank to keep it."
            : "The URL is the secret; it is never shown again."
        }
        required={stored?.url_set !== true}
        error={errors.webhook_url}
        fieldRef={fieldRefs.webhook_url}
      />
      <TransportFields
        formId={formId}
        channel={channel}
        errors={errors}
        fieldRefs={fieldRefs}
      />
    </>
  );
}

function TransportFields({
  formId,
  channel,
  errors,
  fieldRefs,
}: PartProps): ReactNode {
  return (
    <>
      <CheckboxField
        formId={formId}
        name="allow_insecure"
        label="Allow plaintext HTTP. The payload and secret travel unencrypted."
        defaultChecked={channel?.allow_insecure ?? false}
        error={errors.allow_insecure}
        fieldRef={fieldRefs.allow_insecure}
      />
      <CheckboxField
        formId={formId}
        name="allow_private"
        label="Allow a private network address as the destination."
        defaultChecked={channel?.allow_private ?? false}
      />
    </>
  );
}

function EmailFields({
  formId,
  channel,
  errors,
  fieldRefs,
}: PartProps): ReactNode {
  const stored = channel?.kind === "email" ? channel.credentials : undefined;
  return (
    <>
      <TextField
        formId={formId}
        name="smtp_host"
        label="SMTP host"
        defaultValue={channel?.smtp_host}
        error={errors.smtp_host}
        fieldRef={fieldRefs.smtp_host}
      />
      <TextField
        formId={formId}
        name="smtp_port"
        label="SMTP port"
        inputMode="numeric"
        defaultValue={
          channel?.smtp_port === undefined ? "587" : String(channel.smtp_port)
        }
        error={errors.smtp_port}
        fieldRef={fieldRefs.smtp_port}
      />
      <SelectField
        formId={formId}
        name="tls_mode"
        label="TLS"
        options={tlsModeSchema.options.map((mode) => ({
          value: mode,
          label: mode === "starttls" ? "STARTTLS" : "Implicit TLS",
        }))}
        defaultValue={channel?.tls_mode ?? "starttls"}
      />
      <SelectField
        formId={formId}
        name="auth_mode"
        label="Authentication"
        options={authModeSchema.options.map((mode) => ({
          value: mode,
          label: mode.toUpperCase(),
        }))}
        defaultValue={channel?.auth_mode ?? "plain"}
      />
      <TextField
        formId={formId}
        name="username"
        label="SMTP username"
        defaultValue={channel?.username}
        error={errors.username}
        fieldRef={fieldRefs.username}
      />
      <TextField
        formId={formId}
        name="password"
        label="SMTP password"
        type="password"
        hint={
          stored?.password_set ? "Stored. Leave blank to keep it." : undefined
        }
        required={stored?.password_set !== true}
        error={errors.password}
        fieldRef={fieldRefs.password}
      />
      <TextField
        formId={formId}
        name="from_address"
        label="From address"
        type="email"
        defaultValue={channel?.from_address}
        error={errors.from_address}
        fieldRef={fieldRefs.from_address}
      />
      <TextField
        formId={formId}
        name="from_name"
        label="From name"
        required={false}
        defaultValue={channel?.from_name}
        error={undefined}
        fieldRef={undefined}
      />
      <TextField
        formId={formId}
        name="recipients"
        label="Recipients"
        hint="Comma-separated addresses, up to 32."
        defaultValue={channel?.recipients?.join(", ")}
        error={errors.recipients}
        fieldRef={fieldRefs.recipients}
      />
      <CheckboxField
        formId={formId}
        name="allow_private"
        label="Allow a private network address as the SMTP host."
        defaultChecked={channel?.allow_private ?? false}
      />
    </>
  );
}

function TemplateFields({
  formId,
  channel,
  errors,
  fieldRefs,
}: PartProps): ReactNode {
  return (
    <details
      className="template-fields"
      open={
        channel !== undefined &&
        (channel.subject_template !== "" || channel.body_template !== "")
      }
    >
      <summary>Message templates</summary>
      <p className="field-hint">
        Leave blank for the defaults. Fields: {"{{.Title}}"}, {"{{.Kind}}"},{" "}
        {"{{.Status}}"}, {"{{.Requester}}"}, {"{{.Actor}}"}, {"{{.Reason}}"},{" "}
        {"{{.RequestID}}"}, {"{{.OccurredAt}}"}.
      </p>
      <TextField
        formId={formId}
        name="subject_template"
        label="Subject"
        required={false}
        defaultValue={channel?.subject_template}
        error={errors.subject_template}
        fieldRef={fieldRefs.subject_template}
      />
      <BodyField
        formId={formId}
        defaultValue={channel?.body_template}
        error={errors.body_template}
        fieldRef={fieldRefs.body_template}
      />
    </details>
  );
}

function BodyField({
  formId,
  defaultValue,
  error,
  fieldRef,
}: Readonly<{
  formId: string;
  defaultValue: string | undefined;
  error: string | undefined;
  fieldRef: RefObject<HTMLElement | null>;
}>): ReactNode {
  const id = `${formId}-body_template`;
  return (
    <div>
      <label htmlFor={id}>Body</label>
      <textarea
        ref={fieldRef as RefObject<HTMLTextAreaElement | null>}
        id={id}
        name="body_template"
        rows={4}
        defaultValue={defaultValue}
        aria-invalid={error !== undefined}
        aria-describedby={fieldErrorId(formId, "body_template")}
      />
      <FieldError formId={formId} field="body_template" message={error} />
    </div>
  );
}

function TextField({
  formId,
  name,
  label,
  type = "text",
  inputMode,
  placeholder,
  hint,
  required = true,
  defaultValue,
  error,
  fieldRef,
}: Readonly<{
  formId: string;
  name: string;
  label: string;
  type?: "text" | "url" | "password" | "email";
  inputMode?: "numeric" | undefined;
  placeholder?: string | undefined;
  hint?: string | undefined;
  required?: boolean;
  defaultValue?: string | undefined;
  error: string | undefined;
  fieldRef: RefObject<HTMLElement | null> | undefined;
}>): ReactNode {
  const id = `${formId}-${name}`;
  const hintId = `${id}-hint`;
  return (
    <div>
      <label htmlFor={id}>{label}</label>
      {hint === undefined ? null : (
        <p id={hintId} className="field-hint">
          {hint}
        </p>
      )}
      <input
        ref={fieldRef as RefObject<HTMLInputElement | null> | undefined}
        id={id}
        name={name}
        type={type}
        inputMode={inputMode}
        placeholder={placeholder}
        autoComplete="off"
        spellCheck={false}
        required={required}
        defaultValue={defaultValue}
        aria-invalid={error !== undefined}
        aria-describedby={
          hint === undefined
            ? fieldErrorId(formId, name)
            : `${hintId} ${fieldErrorId(formId, name)}`
        }
      />
      <FieldError formId={formId} field={name} message={error} />
    </div>
  );
}

function SelectField({
  formId,
  name,
  label,
  options,
  defaultValue,
}: Readonly<{
  formId: string;
  name: string;
  label: string;
  options: readonly { value: string; label: string }[];
  defaultValue: string;
}>): ReactNode {
  const id = `${formId}-${name}`;
  return (
    <div>
      <label htmlFor={id}>{label}</label>
      <select id={id} name={name} defaultValue={defaultValue}>
        {options.map((option) => (
          <option key={option.value} value={option.value}>
            {option.label}
          </option>
        ))}
      </select>
    </div>
  );
}

function CheckboxField({
  formId,
  name,
  label,
  defaultChecked,
  error,
  fieldRef,
}: Readonly<{
  formId: string;
  name: string;
  label: string;
  defaultChecked: boolean;
  error?: string | undefined;
  fieldRef?: RefObject<HTMLElement | null>;
}>): ReactNode {
  const id = `${formId}-${name}`;
  return (
    <div className="checkbox-field">
      <label htmlFor={id}>
        <input
          ref={fieldRef as RefObject<HTMLInputElement | null> | undefined}
          id={id}
          name={name}
          type="checkbox"
          defaultChecked={defaultChecked}
          aria-invalid={error !== undefined}
          aria-describedby={fieldErrorId(formId, name)}
        />{" "}
        {label}
      </label>
      <FieldError formId={formId} field={name} message={error} />
    </div>
  );
}
