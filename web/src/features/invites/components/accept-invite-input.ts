import { z } from "zod/v4";
import {
  acceptInviteRequestSchema,
  inviteLimits,
  utf8Length,
  type AcceptInviteRequest,
} from "../api/invites-schemas.js";

export type AcceptInviteField = "username" | "password";

export type AcceptInviteFieldErrors = Partial<
  Record<AcceptInviteField, string>
>;

export type ReadAcceptInputResult =
  | Readonly<{ input: AcceptInviteRequest; errors?: undefined }>
  | Readonly<{ input?: undefined; errors: AcceptInviteFieldErrors }>;

const FIELD_ORDER: readonly AcceptInviteField[] = ["username", "password"];

const formSchema = z.object({
  username: z
    .string()
    .trim()
    .min(1, "Choose a username.")
    .refine(
      (value) => utf8Length(value) <= inviteLimits.maxUsernameBytes,
      `Use a username of at most ${String(inviteLimits.maxUsernameBytes)} bytes.`,
    )
    .refine(
      (value) => !/[\p{Cc}]/u.test(value),
      "The username must not contain control characters.",
    ),
  password: z
    .string()
    .min(
      inviteLimits.minPasswordCharacters,
      `Use at least ${String(inviteLimits.minPasswordCharacters)} characters.`,
    )
    .max(
      inviteLimits.maxPasswordCharacters,
      `Use at most ${String(inviteLimits.maxPasswordCharacters)} characters.`,
    ),
});

// Turns the submitted form into the request the server accepts, or into one
// message per field that needs attention. The server applies the full
// username and password policy; this only catches what is certain.
export function readAcceptInviteInput(data: FormData): ReadAcceptInputResult {
  const parsed = formSchema.safeParse({
    username: text(data.get("username")),
    password: text(data.get("password")),
  });
  if (!parsed.success) {
    return { errors: fieldErrors(parsed.error) };
  }
  return { input: acceptInviteRequestSchema.parse(parsed.data) };
}

export function firstInvalidAcceptField(
  errors: AcceptInviteFieldErrors,
): AcceptInviteField | undefined {
  return FIELD_ORDER.find((field) => errors[field] !== undefined);
}

function text(value: FormDataEntryValue | null): string {
  return typeof value === "string" ? value : "";
}

function fieldErrors(error: z.ZodError): AcceptInviteFieldErrors {
  const errors: AcceptInviteFieldErrors = {};
  for (const issue of error.issues) {
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
