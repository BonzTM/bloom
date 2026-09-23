import {
  useEffect,
  useId,
  useRef,
  useState,
  type ReactNode,
  type SyntheticEvent,
} from "react";
import { AsyncStatus } from "../../../components/async-status.js";
import { ApiError, CSRF_REJECTED } from "../../../lib/api/errors.js";
import { FieldError, fieldErrorId } from "../../auth/components/field-error.js";
import {
  quotaLimits,
  requestQuotaInputSchema,
  type RequestQuota,
  type RequestQuotaInput,
} from "../api/quota-schemas.js";
import {
  useRemoveRoleQuota,
  useRoleQuota,
  useSetRoleQuota,
} from "../hooks/roles-queries.js";

type RoleQuotaControlsProps = Readonly<{
  accountId: string;
  roleId: string;
  roleName: string;
}>;

// The request quota of one role: nothing is fetched until the person opens
// it, then the current quota (or "none") is shown with a form to set or
// remove it.
export function RoleQuotaControls({
  accountId,
  roleId,
  roleName,
}: RoleQuotaControlsProps): ReactNode {
  const [open, setOpen] = useState(false);
  if (!open) {
    return (
      <button
        type="button"
        onClick={() => {
          setOpen(true);
        }}
      >
        Quota for {roleName}
      </button>
    );
  }
  return (
    <OpenQuota
      accountId={accountId}
      roleId={roleId}
      roleName={roleName}
      onClose={() => {
        setOpen(false);
      }}
    />
  );
}

// Mounted only while open, so a closed row holds no query at all.
function OpenQuota({
  accountId,
  roleId,
  roleName,
  onClose,
}: RoleQuotaControlsProps & Readonly<{ onClose: () => void }>): ReactNode {
  const quota = useRoleQuota(accountId, roleId);
  if (quota.status === "pending") {
    return <AsyncStatus>Loading the quota…</AsyncStatus>;
  }
  if (quota.status === "error") {
    return (
      <>
        <AsyncStatus kind="alert">The quota could not be loaded.</AsyncStatus>
        <button
          type="button"
          onClick={() => {
            void quota.refetch();
          }}
        >
          Retry
        </button>
      </>
    );
  }
  return (
    <QuotaEditor
      accountId={accountId}
      roleId={roleId}
      roleName={roleName}
      current={quota.data}
      onClose={onClose}
    />
  );
}

type QuotaEditorProps = Readonly<{
  accountId: string;
  roleId: string;
  roleName: string;
  current: RequestQuota | null;
  onClose: () => void;
}>;

type Field = keyof RequestQuotaInput;

const FIELDS: readonly { name: Field; label: string }[] = [
  { name: "movie_limit", label: "Movie limit" },
  { name: "movie_period_days", label: "Movie period (days)" },
  { name: "season_limit", label: "Season limit" },
  { name: "season_period_days", label: "Season period (days)" },
];

function QuotaEditor({
  accountId,
  roleId,
  roleName,
  current,
  onClose,
}: QuotaEditorProps): ReactNode {
  const formId = useId();
  const set = useSetRoleQuota(accountId, roleId);
  const remove = useRemoveRoleQuota(accountId, roleId);
  const [errors, setErrors] = useState<Partial<Record<Field, string>>>({});
  const [saved, setSaved] = useState(false);
  const summaryRef = useRef<HTMLParagraphElement>(null);
  const firstRef = useRef<HTMLInputElement>(null);
  const summary = describeQuotaError(set.error ?? remove.error);
  useEffect(() => {
    if (summary !== undefined) {
      summaryRef.current?.focus();
    }
  }, [summary]);

  function handleSubmit(event: SyntheticEvent<HTMLFormElement>): void {
    event.preventDefault();
    if (set.isPending || remove.isPending) {
      return;
    }
    const result = readQuotaInput(new FormData(event.currentTarget));
    if (result.errors !== undefined) {
      setErrors(result.errors);
      firstRef.current?.focus();
      return;
    }
    setErrors({});
    setSaved(false);
    set.mutate(result.input, {
      onSuccess: () => {
        setSaved(true);
      },
    });
  }

  const summaryId = `${formId}-summary`;
  const busy = set.isPending || remove.isPending;
  return (
    <form
      className="quota-form"
      onSubmit={handleSubmit}
      aria-label={`Request quota for ${roleName}`}
      noValidate
      aria-busy={busy}
      aria-describedby={summary === undefined ? undefined : summaryId}
    >
      {summary === undefined ? null : (
        <p id={summaryId} role="alert" tabIndex={-1} ref={summaryRef}>
          {summary}
        </p>
      )}
      <p className="row-detail">{describeQuota(current)}</p>
      <div className="quota-fields">
        {FIELDS.map((field, index) => (
          <QuotaField
            key={field.name}
            formId={formId}
            name={field.name}
            label={field.label}
            defaultValue={current?.[field.name] ?? 0}
            error={errors[field.name]}
            inputRef={index === 0 ? firstRef : undefined}
          />
        ))}
      </div>
      <span className="row-actions">
        <button type="submit" className="btn-primary" disabled={busy}>
          {set.isPending ? "Saving…" : "Save quota"}
        </button>
        {current === null ? null : (
          <button
            type="button"
            className="btn-danger"
            disabled={busy}
            onClick={() => {
              setSaved(false);
              remove.mutate();
            }}
          >
            {remove.isPending ? "Removing…" : "Remove quota"}
          </button>
        )}
        <button type="button" className="btn-ghost" onClick={onClose}>
          Close quota for {roleName}
        </button>
      </span>
      <p role="status">{saved ? `Quota for ${roleName} saved.` : ""}</p>
    </form>
  );
}

function QuotaField({
  formId,
  name,
  label,
  defaultValue,
  error,
  inputRef,
}: Readonly<{
  formId: string;
  name: Field;
  label: string;
  defaultValue: number;
  error: string | undefined;
  inputRef: React.RefObject<HTMLInputElement | null> | undefined;
}>): ReactNode {
  const id = `${formId}-${name}`;
  return (
    <div>
      <label htmlFor={id}>{label}</label>
      <input
        ref={inputRef}
        id={id}
        name={name}
        type="text"
        inputMode="numeric"
        autoComplete="off"
        defaultValue={String(defaultValue)}
        aria-invalid={error !== undefined}
        aria-describedby={fieldErrorId(formId, name)}
      />
      <FieldError formId={formId} field={name} message={error} />
    </div>
  );
}

// "2 movies per 30 days, seasons unlimited", or "No quota".
export function describeQuota(quota: RequestQuota | null): string {
  if (quota === null) {
    return "No quota: this role is not limited.";
  }
  const movies =
    quota.movie_limit === 0 && quota.movie_period_days === 0
      ? "movies unlimited"
      : `${String(quota.movie_limit)} movies per ${String(quota.movie_period_days)} days`;
  const seasons =
    quota.season_limit === 0 && quota.season_period_days === 0
      ? "seasons unlimited"
      : `${String(quota.season_limit)} seasons per ${String(quota.season_period_days)} days`;
  return `Current quota: ${movies}, ${seasons}.`;
}

type ReadResult =
  | Readonly<{ input: RequestQuotaInput; errors?: undefined }>
  | Readonly<{ input?: undefined; errors: Partial<Record<Field, string>> }>;

// Whole numbers only; a period is at most ten years.
export function readQuotaInput(data: FormData): ReadResult {
  const raw: Record<Field, unknown> = {
    movie_limit: readCount(data.get("movie_limit")),
    movie_period_days: readCount(data.get("movie_period_days")),
    season_limit: readCount(data.get("season_limit")),
    season_period_days: readCount(data.get("season_period_days")),
  };
  const parsed = requestQuotaInputSchema.safeParse(raw);
  if (parsed.success) {
    return { input: parsed.data };
  }
  const errors: Partial<Record<Field, string>> = {};
  for (const issue of parsed.error.issues) {
    const field = issue.path[0];
    if (isField(field) && errors[field] === undefined) {
      errors[field] = field.endsWith("_days")
        ? `Enter a whole number of days from 0 to ${String(quotaLimits.maxPeriodDays)}.`
        : "Enter a whole number of 0 or more.";
    }
  }
  return { errors };
}

function readCount(value: FormDataEntryValue | null): unknown {
  const text = typeof value === "string" ? value.trim() : "";
  return /^[0-9]{1,9}$/.test(text) ? Number(text) : text;
}

function isField(value: unknown): value is Field {
  return FIELDS.some((field) => field.name === value);
}

function describeQuotaError(error: unknown): string | undefined {
  if (error === null || error === undefined) {
    return undefined;
  }
  if (!(error instanceof ApiError)) {
    return "The quota could not be changed.";
  }
  if (error.status === 403 && error.code === CSRF_REJECTED) {
    return "The request was refused as cross-site. Reload the page and try again.";
  }
  switch (error.status) {
    case 401:
      return "Your sign-in could not be confirmed. Sign in again and retry.";
    case 403:
      return "You no longer have permission to change role quotas.";
    case 404:
      return "That role or quota no longer exists.";
    case 422:
      return "Check the quota values and try again.";
    case undefined:
    default:
      return error.kind === "network"
        ? "Bloom could not be reached. Check your connection and try again."
        : "The quota could not be changed.";
  }
}
