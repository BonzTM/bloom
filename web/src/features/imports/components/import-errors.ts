import { ApiError, CSRF_REJECTED } from "../../../lib/api/errors.js";
import {
  IMPORT_IN_PROGRESS,
  type ImportSource,
} from "../api/imports-schemas.js";

const messages: Readonly<Record<number, string>> = {
  401: "Your sign-in could not be confirmed. Sign in again and retry.",
  403: "You no longer have permission to manage imports.",
  404: "That import no longer exists.",
  409: "An import for that server and source is already waiting or running.",
  413: "The file is larger than 256 MiB.",
  415: "The file is not a format Bloom imports.",
  422: "Check the fields and try again.",
};

// A refused file is the usual cause of a 422 on a file import, so the
// message says which file that source takes.
const fileSourceRefusals: Readonly<Partial<Record<ImportSource, string>>> = {
  bloom_export:
    "Check the server and the file. A Bloom import takes the .zip another Bloom exported; export an older or uncompressed archive again.",
  jellystat:
    "Check the server and the file. A Jellystat import takes the .jsonl backup Jellystat writes under Settings, Backup.",
};

export function describeImportError(
  error: unknown,
  source?: ImportSource,
): string | undefined {
  if (error === null || error === undefined) {
    return undefined;
  }
  if (!(error instanceof ApiError)) {
    return "The import could not be changed.";
  }
  if (error.status === 403 && error.code === CSRF_REJECTED) {
    return "The request was refused as cross-site. Reload the page and try again.";
  }
  if (error.code === IMPORT_IN_PROGRESS) {
    return messages[409];
  }
  const refusal = source === undefined ? undefined : fileSourceRefusals[source];
  if (error.status === 422 && refusal !== undefined) {
    return refusal;
  }
  if (error.status !== undefined && error.status in messages) {
    return messages[error.status];
  }
  if (error.kind === "network") {
    return "Bloom could not be reached. Check your connection and try again.";
  }
  return "The import could not be changed.";
}
