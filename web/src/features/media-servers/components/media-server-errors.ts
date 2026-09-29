import {
  ApiError,
  CSRF_REJECTED,
  type ProbeFailureReason,
} from "../../../lib/api/errors.js";

const GENERIC_REGISTER_FAILURE =
  "The server could not be registered. Please try again.";
const GENERIC_REMOVE_FAILURE =
  "The server could not be removed. Please try again.";
const UNREACHABLE =
  "Bloom could not be reached. Check your connection and try again.";
// The server keeps working after the browser stops waiting, so the outcome
// is unknown here rather than failed.
const TIMED_OUT =
  "The server took too long to answer, so the outcome is unknown. Check the list below before trying again.";
const CROSS_SITE =
  "The request was refused as cross-site. Reload the page and try again.";

const registerMessagesByStatus: Readonly<Record<number, string>> = {
  401: "Your sign-in could not be confirmed. Sign in again and retry.",
  403: "You no longer have permission to manage media servers.",
  409: "Another media server already uses that name.",
  422: "Check the name, address, and API key. HTTPS is required unless plaintext HTTP is allowed, and that override applies to http:// addresses only.",
  502: "The server could not be checked. Nothing was saved.",
};

// What went wrong when Bloom probed the server, when the API says which.
const probeMessagesByReason: Readonly<Record<ProbeFailureReason, string>> = {
  unreachable:
    "Bloom could not reach the server at that address. Check the address and port, and that the network between Bloom and the server allows it. Nothing was saved.",
  unauthorized: "The server rejected the API key. Nothing was saved.",
  not_found:
    "The address answered, but Jellyfin's API was not found there. Check that the address is the server itself, including any base path. Nothing was saved.",
  malformed:
    "The address answered, but not like a Jellyfin server. Check that it points at Jellyfin itself rather than a login page or a redirect. Nothing was saved.",
  unavailable: "The server is not answering right now. Try again in a moment.",
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
  if (error.status === 502 && error.reason !== undefined) {
    return probeMessagesByReason[error.reason];
  }
  if (error.status !== undefined && error.status in messagesByStatus) {
    return messagesByStatus[error.status];
  }
  if (error.kind === "network") {
    return UNREACHABLE;
  }
  if (error.kind === "aborted") {
    return TIMED_OUT;
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
