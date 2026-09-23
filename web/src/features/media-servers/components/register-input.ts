import type { ZodError } from "zod/v4";
import {
  registerMediaServerInputSchema,
  type RegisterMediaServerInput,
} from "../api/media-servers-schemas.js";

export type RegisterField = "name" | "base_url" | "api_key" | "allow_insecure";

export type RegisterFieldErrors = Partial<Record<RegisterField, string>>;

export type ReadInputResult =
  | Readonly<{ input: RegisterMediaServerInput; errors?: undefined }>
  | Readonly<{ input?: undefined; errors: RegisterFieldErrors }>;

const REGISTER_FIELDS: readonly RegisterField[] = [
  "name",
  "base_url",
  "api_key",
  "allow_insecure",
];

// Turns the submitted form into a typed request, or into one message per
// field that needs attention. The transport rule is cross-field: plaintext
// HTTP needs the explicit override, and the override means nothing for HTTPS.
export function readRegisterInput(data: FormData): ReadInputResult {
  const parsed = registerMediaServerInputSchema.safeParse({
    kind: "jellyfin",
    name: text(data.get("name")),
    base_url: text(data.get("base_url")),
    api_key: text(data.get("api_key")),
    allow_insecure: data.get("allow_insecure") === "on",
  });
  if (!parsed.success) {
    return { errors: fieldErrors(parsed.error) };
  }
  const transport = transportErrors(parsed.data);
  if (transport !== undefined) {
    return { errors: transport };
  }
  return { input: parsed.data };
}

function text(value: FormDataEntryValue | null): string {
  return typeof value === "string" ? value : "";
}

function fieldErrors(error: ZodError): RegisterFieldErrors {
  const errors: RegisterFieldErrors = {};
  for (const issue of error.issues) {
    const field = issue.path[0];
    if (isRegisterField(field) && errors[field] === undefined) {
      errors[field] = issue.message;
    }
  }
  return errors;
}

function isRegisterField(value: unknown): value is RegisterField {
  return (
    typeof value === "string" &&
    REGISTER_FIELDS.includes(value as RegisterField)
  );
}

function transportErrors(
  input: RegisterMediaServerInput,
): RegisterFieldErrors | undefined {
  const plaintext = input.base_url.startsWith("http://");
  if (plaintext && !input.allow_insecure) {
    return {
      base_url:
        "Plaintext HTTP sends the API key unencrypted. Use https://, or allow plaintext HTTP below.",
    };
  }
  if (!plaintext && input.allow_insecure) {
    return {
      allow_insecure: "This override applies to http:// addresses only.",
    };
  }
  return undefined;
}
