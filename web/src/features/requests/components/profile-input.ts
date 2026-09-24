import { z } from "zod/v4";
import {
  MANAGER_KINDS,
  type DownloadManager,
} from "../api/download-manager-schemas.js";
import {
  requestLimits,
  requestProfileInputSchema,
  utf8Length,
  type RequestProfileInput,
} from "../api/requests-schemas.js";

export type ProfileField =
  "name" | "download_manager" | "quality_profile" | "root_folder" | "tags";

export type ProfileFieldErrors = Partial<Record<ProfileField, string>>;

export type ReadProfileResult =
  | Readonly<{ input: RequestProfileInput; errors?: undefined }>
  | Readonly<{ input?: undefined; errors: ProfileFieldErrors }>;

const FIELD_ORDER: readonly ProfileField[] = [
  "name",
  "download_manager",
  "quality_profile",
  "root_folder",
  "tags",
];

const field = (label: string, max: number) =>
  z
    .string()
    .trim()
    .min(1, `Choose the ${label}.`)
    .refine(
      (value) => utf8Length(value) <= max,
      `Use at most ${String(max)} bytes for the ${label}.`,
    );

const formSchema = z.object({
  name: z
    .string()
    .trim()
    .min(1, "Enter the profile name.")
    .refine(
      (value) => utf8Length(value) <= requestLimits.maxNameBytes,
      `Use at most ${String(requestLimits.maxNameBytes)} bytes for the profile name.`,
    ),
  download_manager: z.string().min(1, "Choose a download manager instance."),
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
// message per field that needs attention. The instance is chosen by id from
// the registered managers; its kind decides which media the profile takes.
export function readProfileInput(
  data: FormData,
  managers: readonly DownloadManager[],
): ReadProfileResult {
  const parsed = formSchema.safeParse({
    name: text(data.get("name")),
    download_manager: text(data.get("download_manager")),
    quality_profile: text(data.get("quality_profile")),
    root_folder: text(data.get("root_folder")),
    tags: data.getAll("tags").filter((value) => typeof value === "string"),
  });
  if (!parsed.success) {
    return { errors: fieldErrors(parsed.error) };
  }
  const manager = managers.find((m) => m.id === parsed.data.download_manager);
  if (manager === undefined) {
    return {
      errors: { download_manager: "Choose a registered download manager." },
    };
  }
  return {
    input: requestProfileInputSchema.parse({
      name: parsed.data.name,
      kinds: [MANAGER_KINDS[manager.kind]],
      download_manager_kind: manager.kind,
      download_manager_instance: manager.name,
      quality_profile: parsed.data.quality_profile,
      root_folder: parsed.data.root_folder,
      tags: parsed.data.tags,
    }),
  };
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
