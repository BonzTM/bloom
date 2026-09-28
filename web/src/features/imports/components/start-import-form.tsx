import {
  useEffect,
  useId,
  useRef,
  useState,
  type ReactNode,
  type SyntheticEvent,
} from "react";
import { FieldError, fieldErrorId } from "../../auth/components/field-error.js";
import {
  importSourceSchema,
  MAX_IMPORT_UPLOAD_BYTES,
  mediaServerIdSchema,
  type ImportSource,
  type StartImportInput,
} from "../api/imports-schemas.js";
import { describeImportError } from "./import-errors.js";

type ServerOption = Readonly<{ id: string; name: string }>;

type StartImportFormProps = Readonly<{
  servers: readonly ServerOption[];
  pending: boolean;
  serverError: unknown;
  onSubmit: (input: StartImportInput) => void;
}>;

type Field = "media_server_id" | "source" | "file";
type FieldErrors = Partial<Record<Field, string>>;

// Uncontrolled apart from the source, which decides whether a file is asked
// for. A rejected submit keeps what was chosen.
export function StartImportForm({
  servers,
  pending,
  serverError,
  onSubmit,
}: StartImportFormProps): ReactNode {
  const formId = useId();
  const summaryRef = useRef<HTMLParagraphElement>(null);
  const [source, setSource] = useState<ImportSource>("playback_reporting");
  const [errors, setErrors] = useState<FieldErrors>({});
  const summary = describeImportError(serverError);

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
    const result = readStartInput(
      new FormData(event.currentTarget),
      chosenFile(event.currentTarget),
    );
    if ("errors" in result) {
      setErrors(result.errors);
      return;
    }
    setErrors({});
    onSubmit(result.input);
  }

  return (
    <form aria-label="Start an import" onSubmit={handleSubmit} noValidate>
      {summary === undefined ? null : (
        <p ref={summaryRef} tabIndex={-1} role="alert" className="form-error">
          {summary}
        </p>
      )}
      <div className="field">
        <label htmlFor={`${formId}-server`}>Server</label>
        <select
          id={`${formId}-server`}
          name="media_server_id"
          required
          aria-describedby={fieldErrorId(formId, "media_server_id")}
          aria-invalid={errors.media_server_id !== undefined}
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
      <fieldset className="field">
        <legend>Source</legend>
        <label className="choice">
          <input
            type="radio"
            name="source"
            value="playback_reporting"
            checked={source === "playback_reporting"}
            onChange={() => {
              setSource("playback_reporting");
            }}
          />{" "}
          Playback Reporting plugin on that server
        </label>
        <label className="choice">
          <input
            type="radio"
            name="source"
            value="bloom_export"
            checked={source === "bloom_export"}
            onChange={() => {
              setSource("bloom_export");
            }}
          />{" "}
          A Bloom export file for that server
        </label>
        <FieldError formId={formId} field="source" message={errors.source} />
      </fieldset>
      {source === "bloom_export" ? (
        <div className="field">
          <label htmlFor={`${formId}-file`}>Export file</label>
          <input
            id={`${formId}-file`}
            type="file"
            name="file"
            accept=".zip,.jsonl,application/zip,application/x-ndjson"
            aria-describedby={`${fieldErrorId(formId, "file")} ${formId}-file-hint`}
            aria-invalid={errors.file !== undefined}
          />
          <p id={`${formId}-file-hint`} className="field-hint">
            The zip another Bloom exported (or an older JSON Lines export), at
            most 256 MiB.
          </p>
          <FieldError formId={formId} field="file" message={errors.file} />
        </div>
      ) : null}
      <button type="submit" disabled={pending}>
        {pending ? "Starting…" : "Start import"}
      </button>
    </form>
  );
}

// The chosen file comes from the input itself: a form's FormData carries
// only a copy, and not every runtime copies the contents.
function chosenFile(form: HTMLFormElement): File | undefined {
  const input = form.elements.namedItem("file");
  return input instanceof HTMLInputElement ? input.files?.[0] : undefined;
}

// Reads the submitted form into a request, or the field messages to show.
export function readStartInput(
  data: FormData,
  file: File | undefined,
): { input: StartImportInput } | { errors: FieldErrors } {
  const errors: FieldErrors = {};
  const serverValue = data.get("media_server_id");
  const server = mediaServerIdSchema.safeParse(
    typeof serverValue === "string" ? serverValue : "",
  );
  if (!server.success) {
    errors.media_server_id = "Choose the server the history belongs to.";
  }
  const source = importSourceSchema.safeParse(data.get("source"));
  if (!source.success) {
    errors.source = "Choose where the history comes from.";
  }
  if (source.success && source.data === "bloom_export") {
    if (file === undefined || file.size === 0) {
      errors.file = "Choose the export file.";
    } else if (file.size > MAX_IMPORT_UPLOAD_BYTES) {
      errors.file = "The file is larger than 256 MiB.";
    }
  }
  if (!server.success || !source.success || Object.keys(errors).length > 0) {
    return { errors };
  }
  if (source.data === "bloom_export" && file !== undefined) {
    return {
      input: { source: "bloom_export", mediaServerId: server.data, file },
    };
  }
  return {
    input: { source: "playback_reporting", mediaServerId: server.data },
  };
}
