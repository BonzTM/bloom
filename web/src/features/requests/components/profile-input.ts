import { z } from "zod/v4";
import {
  mediaKindSchema,
  requestLimits,
  requestProfileInputSchema,
  utf8Length,
  type RequestProfileInput,
} from "../api/requests-schemas.js";

export type ProfileField =
  | "name"
  | "kinds"
  | "download_manager_kind"
  | "download_manager_instance"
  | "quality_profile"
  | "root_folder"
  | "tags";

export type ProfileFieldErrors = Partial<Record<ProfileField, string>>;

export type ReadProfileResult =
  | Readonly<{ input: RequestProfileInput; errors?: undefined }>
  | Readonly<{ input?: undefined; errors: ProfileFieldErrors }>;

const FIELD_ORDER: readonly ProfileField[] = [
  "name",
  "kinds",
  "download_manager_kind",
  "download_manager_instance",
  "quality_profile",
  "root_folder",
  "tags",
];

const field = (label: string, max: number) =>
  z
    .string()
    .trim()
    .min(1, `Enter the ${label}.`)
    .refine(
      (value) => utf8Length(value) <= max,
      `Use at most ${String(max)} bytes for the ${label}.`,
    );

const formSchema = z.object({
  name: field("profile name", requestLimits.maxNameBytes),
  kinds: z
    .array(mediaKindSchema)
    .min(1, "Choose at least one kind of media this profile accepts."),
  download_manager_kind: field(
    "download manager kind",
    requestLimits.maxFieldBytes,
  ),
  download_manager_instance: field(
    "download manager instance",
    requestLimits.maxFieldBytes,
  ),
  quality_profile: field("quality profile", requestLimits.maxFieldBytes),
  root_folder: field("root folder", requestLimits.maxFieldBytes),
  tags: z
    .array(
      z
        .string()
        .trim()
        .min(1)
        .refine(
          (value) => utf8Length(value) <= requestLimits.maxTagBytes,
          `Use at most ${String(requestLimits.maxTagBytes)} bytes per tag.`,
        ),
    )
    .refine(
      (tags) => new Set(tags).size === tags.length,
      "Each tag may appear only once.",
    ),
});

// Turns the submitted form into the request the server accepts, or into one
// message per field that needs attention. Tags are typed comma-separated.
export function readProfileInput(data: FormData): ReadProfileResult {
  const parsed = formSchema.safeParse({
    name: text(data.get("name")),
    kinds: data.getAll("kinds").filter((value) => typeof value === "string"),
    download_manager_kind: text(data.get("download_manager_kind")),
    download_manager_instance: text(data.get("download_manager_instance")),
    quality_profile: text(data.get("quality_profile")),
    root_folder: text(data.get("root_folder")),
    tags: text(data.get("tags"))
      .split(",")
      .map((tag) => tag.trim())
      .filter((tag) => tag !== ""),
  });
  if (!parsed.success) {
    return { errors: fieldErrors(parsed.error) };
  }
  return { input: requestProfileInputSchema.parse(parsed.data) };
}

export function firstInvalidProfileField(
  errors: ProfileFieldErrors,
): ProfileField | undefined {
  return FIELD_ORDER.find((name) => errors[name] !== undefined);
}

function text(value: FormDataEntryValue | null): string {
  return typeof value === "string" ? value : "";
}

function fieldErrors(error: z.ZodError): ProfileFieldErrors {
  const errors: ProfileFieldErrors = {};
  for (const issue of error.issues) {
    const name = issue.path[0];
    if (isField(name) && errors[name] === undefined) {
      errors[name] = issue.message;
    }
  }
  return errors;
}

function isField(value: unknown): value is ProfileField {
  return (
    typeof value === "string" && FIELD_ORDER.includes(value as ProfileField)
  );
}
