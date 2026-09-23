import { z } from "zod/v4";
import {
  createInviteRequestSchema,
  inviteLimits,
  utf8Length,
  type CreateInviteRequest,
} from "../api/invites-schemas.js";

export type CreateInviteField =
  "media_server_id" | "label" | "expiry" | "max_uses";

export type CreateInviteFieldErrors = Partial<
  Record<CreateInviteField, string>
>;

export type ReadCreateInputResult =
  | Readonly<{ input: CreateInviteRequest; errors?: undefined }>
  | Readonly<{ input?: undefined; errors: CreateInviteFieldErrors }>;

// Expiry is chosen from a short list rather than typed as a date: the
// choices cover what an operator means, and the timestamp is computed here
// from the injected clock so the form stays deterministic.
export const EXPIRY_CHOICES = [
  { value: "never", label: "Never", days: undefined },
  { value: "day", label: "1 day", days: 1 },
  { value: "week", label: "7 days", days: 7 },
  { value: "month", label: "30 days", days: 30 },
] as const;

type ExpiryValue = (typeof EXPIRY_CHOICES)[number]["value"];

const FIELD_ORDER: readonly CreateInviteField[] = [
  "media_server_id",
  "label",
  "expiry",
  "max_uses",
];

const formSchema = z.object({
  media_server_id: z.uuid("Choose the server this invite is for."),
  label: z
    .string()
    .trim()
    .min(1, "Enter a label so this invite can be told apart later.")
    .refine(
      (value) => utf8Length(value) <= inviteLimits.maxLabelBytes,
      `Use a label of at most ${String(inviteLimits.maxLabelBytes)} bytes.`,
    ),
  expiry: z.enum(
    EXPIRY_CHOICES.map((choice) => choice.value) as [
      ExpiryValue,
      ...ExpiryValue[],
    ],
    "Choose when the invite expires.",
  ),
  max_uses: z
    .string()
    .trim()
    .regex(/^[0-9]*$/u, "Enter a whole number of uses, or leave it empty.")
    .transform((digits) => (digits === "" ? undefined : Number(digits)))
    .pipe(
      z
        .number()
        .int()
        .min(1, "Allow at least one use, or leave it empty for no limit.")
        .max(
          inviteLimits.maxUses,
          `Allow at most ${String(inviteLimits.maxUses)} uses.`,
        )
        .optional(),
    ),
});

// Turns the submitted form into the request the server accepts, or into one
// message per field that needs attention.
export function readCreateInviteInput(
  data: FormData,
  now: Date,
): ReadCreateInputResult {
  const parsed = formSchema.safeParse({
    media_server_id: text(data.get("media_server_id")),
    label: text(data.get("label")),
    expiry: text(data.get("expiry")),
    max_uses: text(data.get("max_uses")),
  });
  if (!parsed.success) {
    return { errors: fieldErrors(parsed.error) };
  }
  const request: CreateInviteRequest = {
    media_server_id: parsed.data.media_server_id,
    label: parsed.data.label,
  };
  const expiresAt = expiryTimestamp(parsed.data.expiry, now);
  if (expiresAt !== undefined) {
    request.expires_at = expiresAt;
  }
  if (parsed.data.max_uses !== undefined) {
    request.max_uses = parsed.data.max_uses;
  }
  return { input: createInviteRequestSchema.parse(request) };
}

export function firstInvalidField(
  errors: CreateInviteFieldErrors,
): CreateInviteField | undefined {
  return FIELD_ORDER.find((field) => errors[field] !== undefined);
}

function expiryTimestamp(value: ExpiryValue, now: Date): string | undefined {
  const choice = EXPIRY_CHOICES.find((candidate) => candidate.value === value);
  if (choice?.days === undefined) {
    return undefined;
  }
  const expires = new Date(now.getTime() + choice.days * 24 * 60 * 60 * 1000);
  return expires.toISOString();
}

function text(value: FormDataEntryValue | null): string {
  return typeof value === "string" ? value : "";
}

function fieldErrors(error: z.ZodError): CreateInviteFieldErrors {
  const errors: CreateInviteFieldErrors = {};
  for (const issue of error.issues) {
    const field = issue.path[0];
    if (isField(field) && errors[field] === undefined) {
      errors[field] = issue.message;
    }
  }
  return errors;
}

function isField(value: unknown): value is CreateInviteField {
  return (
    typeof value === "string" &&
    FIELD_ORDER.includes(value as CreateInviteField)
  );
}
