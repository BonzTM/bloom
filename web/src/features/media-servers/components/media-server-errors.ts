import { ApiError, CSRF_REJECTED } from "../../../lib/api/errors.js";

const GENERIC_REGISTER_FAILURE =
  "The server could not be registered. Please try again.";
const GENERIC_REMOVE_FAILURE =
  "The server could not be removed. Please try again.";
const UNREACHABLE =
  "Bloom could not be reached. Check your connection and try again.";
const CROSS_SITE =
  "The request was refused as cross-site. Reload the page and try again.";

const registerMessagesByStatus: Readonly<Record<number, string>> = {
  401: "Your sign-in could not be confirmed. Sign in again and retry.",
  403: "You no longer have permission to manage media servers.",
  409: "Another media server already uses that name.",
  422: "Check the name, address, and API key. HTTPS is required unless plaintext HTTP is allowed, and that override applies to http:// addresses only.",
  502: "The server did not answer as a Jellyfin server, or it rejected the API key. Nothing was saved.",
};

const removeMessagesByStatus: Readonly<Record<number, string>> = {
  401: "Your sign-in could not be confirmed. Sign in again and retry.",
  403: "You no longer have permission to manage media servers.",
  404: "That server was already removed.",
};

// Maps a failed registration into one sentence a person can act on. The
// server keeps upstream details to itself, and so does this text.
export function describeRegisterError(error: unknown): string | undefined {
  return describe(error, registerMessagesByStatus, GENERIC_REGISTER_FAILURE);
}

export function describeRemoveError(error: unknown): string | undefined {
  return describe(error, removeMessagesByStatus, GENERIC_REMOVE_FAILURE);
}

function describe(
  error: unknown,
  messagesByStatus: Readonly<Record<number, string>>,
  fallback: string,
): string | undefined {
  if (error === null || error === undefined) {
    return undefined;
  }
  if (!(error instanceof ApiError)) {
    return fallback;
  }
  if (error.status === 503) {
    return describeBusy(error.retryAfterSeconds);
  }
  if (error.status === 403 && error.code === CSRF_REJECTED) {
    return CROSS_SITE;
  }
  if (error.status !== undefined && error.status in messagesByStatus) {
    return messagesByStatus[error.status];
  }
  if (error.kind === "network") {
    return UNREACHABLE;
  }
  return fallback;
}

function describeBusy(retryAfterSeconds: number | undefined): string {
  if (retryAfterSeconds === undefined || retryAfterSeconds < 1) {
    return "That server is busy right now. Please wait a moment and try again.";
  }
  const unit = retryAfterSeconds === 1 ? "second" : "seconds";
  return `That server is busy right now. Try again in ${String(retryAfterSeconds)} ${unit}.`;
}
