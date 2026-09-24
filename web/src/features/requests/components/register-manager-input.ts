import type { ZodError } from "zod/v4";
import {
  registerDownloadManagerRequestSchema,
  type RegisterDownloadManagerRequest,
} from "../api/download-manager-schemas.js";

export type ManagerField =
  "kind" | "name" | "base_url" | "api_key" | "allow_insecure";

export type ManagerFieldErrors = Partial<Record<ManagerField, string>>;

export type ReadManagerResult =
  | Readonly<{ input: RegisterDownloadManagerRequest; errors?: undefined }>
  | Readonly<{ input?: undefined; errors: ManagerFieldErrors }>;

export const MANAGER_FIELDS: readonly ManagerField[] = [
  "kind",
  "name",
  "base_url",
  "api_key",
  "allow_insecure",
];

const MESSAGES: Readonly<Record<ManagerField, string>> = {
  kind: "Choose Radarr or Sonarr.",
  name: "Enter a name of at most 100 bytes.",
  base_url: "Enter the address as a URL starting with http:// or https://.",
  api_key: "Enter the API key without control characters.",
  allow_insecure: "Plaintext HTTP needs the explicit override.",
};

// Turns the submitted form into the registration the server accepts, or
// into one message per field. The transport rule is cross-field: plaintext
// HTTP needs the override, and the override means nothing for HTTPS.
export function readManagerInput(data: FormData): ReadManagerResult {
  const parsed = registerDownloadManagerRequestSchema.safeParse({
    kind: text(data.get("kind")),
    name: text(data.get("name")).trim(),
    base_url: text(data.get("base_url")).trim(),
    api_key: text(data.get("api_key")).trim(),
    allow_insecure: data.get("allow_insecure") === "on",
  });
  if (!parsed.success) {
    return { errors: fieldErrors(parsed.error) };
  }
  const plaintext = parsed.data.base_url.toLowerCase().startsWith("http://");
  if (plaintext && !parsed.data.allow_insecure) {
    return { errors: { allow_insecure: MESSAGES.allow_insecure } };
  }
  if (!plaintext && parsed.data.allow_insecure) {
    return {
      errors: { allow_insecure: "The override applies to http:// only." },
    };
  }
  return { input: parsed.data };
}

export function firstInvalidManagerField(
  errors: ManagerFieldErrors,
): ManagerField | undefined {
  return MANAGER_FIELDS.find((field) => errors[field] !== undefined);
}

function text(value: FormDataEntryValue | null): string {
  return typeof value === "string" ? value : "";
}

function fieldErrors(error: ZodError): ManagerFieldErrors {
  const errors: ManagerFieldErrors = {};
  for (const issue of error.issues) {
    const field = issue.path[0];
    if (isField(field) && errors[field] === undefined) {
      errors[field] = MESSAGES[field];
    }
  }
  return errors;
}

function isField(value: unknown): value is ManagerField {
  return (
    typeof value === "string" && MANAGER_FIELDS.includes(value as ManagerField)
  );
}
