import {
  ApiError,
  CSRF_REJECTED,
  type ProbeFailureReason,
} from "../../../lib/api/errors.js";

// The server keeps working after the browser stops waiting, so the outcome
// is unknown here rather than failed.
const TIMED_OUT =
  "That took too long to answer, so the outcome is unknown. Check the list before trying again.";
const UNREACHABLE =
  "Bloom could not be reached. Check your connection and try again.";
const CROSS_SITE =
  "The request was refused as cross-site. Reload the page and try again.";
const SIGN_IN_AGAIN =
  "Your sign-in could not be confirmed. Sign in again and retry.";

type Messages = Readonly<Record<number, string>>;

const profileMessages: Messages = {
  401: SIGN_IN_AGAIN,
  403: "You no longer have permission to manage request profiles.",
  404: "That profile no longer exists.",
  409: "Another profile already uses that name, or requests still reference this one.",
  422: "Check the profile fields; the instance must exist and accept this kind of media.",
};

const decisionMessages: Messages = {
  401: SIGN_IN_AGAIN,
  403: "You no longer have permission to decide requests.",
  404: "That request no longer exists.",
  409: "That request cannot be changed from its current state.",
  422: "Check the reason and try again.",
};

const keyMessages: Messages = {
  401: SIGN_IN_AGAIN,
  403: "You no longer have permission to change settings.",
  404: "No TMDB token is stored.",
  422: "TMDB needs the API Read Access Token, not the v3 API key, without control characters.",
};

const searchMessages: Messages = {
  401: SIGN_IN_AGAIN,
  403: "You no longer have permission to search for titles.",
  404: "That title was not found.",
  422: "Enter something to search for, up to 200 characters.",
  502: "The metadata provider answered with something Bloom could not use.",
  503: "Searching is not available right now. Try again in a moment.",
};

const createMessages: Messages = {
  401: SIGN_IN_AGAIN,
  403: "You no longer have permission to request titles.",
  404: "That title or profile no longer exists.",
  409: "This title is already requested.",
  422: "Check the request and try again.",
  502: "The metadata provider answered with something Bloom could not use.",
  503: "Requests are not available right now. Try again in a moment.",
};

const QUOTA_EXCEEDED = "request_quota_exceeded";
const METADATA_NOT_CONFIGURED = "metadata_not_configured";

const providerReasonMessages: Readonly<Record<string, string>> = {
  unreachable:
    "Bloom could not reach TMDB. The server may not be allowed to reach api.themoviedb.org.",
  unauthorized: "TMDB rejected Bloom's API Read Access Token.",
  malformed: "TMDB answered with something Bloom could not use.",
  unavailable: "TMDB is not answering right now. Try again in a moment.",
};

export function describeSearchError(error: unknown): string | undefined {
  if (error instanceof ApiError && error.code === METADATA_NOT_CONFIGURED) {
    return "Searching needs a TMDB key, which no administrator has set yet.";
  }
  if (error instanceof ApiError && error.reason !== undefined) {
    const byReason = providerReasonMessages[error.reason];
    if (byReason !== undefined) {
      return byReason;
    }
  }
  if (error instanceof ApiError && error.kind === "aborted") {
    return "Bloom took too long to answer. If this keeps happening, the server may not be able to reach TMDB.";
  }
  return describe(error, searchMessages, "The search could not be completed.");
}

export function describeCreateError(error: unknown): string | undefined {
  if (error instanceof ApiError && error.code === QUOTA_EXCEEDED) {
    return "You have reached your request limit for now.";
  }
  if (error instanceof ApiError && error.code === METADATA_NOT_CONFIGURED) {
    return "Requesting needs a TMDB key, which no administrator has set yet.";
  }
  return describe(error, createMessages, "The request could not be created.");
}

const managerMessages: Messages = {
  401: SIGN_IN_AGAIN,
  403: "You no longer have permission to manage download managers.",
  404: "That download manager no longer exists.",
  409: "Another instance already uses that name, or a request profile still references this one.",
  422: "Check the instance fields and try again.",
  502: "The instance could not be checked.",
  503: "The instance is busy. Try again in a moment.",
};

const managerProbeMessages: Readonly<Record<ProbeFailureReason, string>> = {
  unreachable:
    "Bloom could not reach the instance at that address. Check the address and port, and that the network between Bloom and the instance allows it.",
  unauthorized:
    "The instance rejected the API key. Check it against Settings, General in Radarr or Sonarr, and make sure the address is the instance itself rather than a proxy or sign-on in front of it.",
  not_found:
    "The address answered, but the instance's API was not found there. Check that the address is the instance itself, including any base path.",
  malformed:
    "The address answered, but not like a Radarr or Sonarr instance: a login page, a proxy, or a redirect is in the way. Use the instance's own address and port.",
  unavailable:
    "The instance is not answering right now. Try again in a moment.",
};

export function describeManagerError(error: unknown): string | undefined {
  if (
    error instanceof ApiError &&
    error.status === 502 &&
    error.reason !== undefined
  ) {
    return managerProbeMessages[error.reason];
  }
  return describe(
    error,
    managerMessages,
    "The download manager could not be changed.",
  );
}

export function describeProfileError(error: unknown): string | undefined {
  return describe(error, profileMessages, "The profile could not be saved.");
}

export function describeDecisionError(error: unknown): string | undefined {
  return describe(error, decisionMessages, "The decision could not be saved.");
}

export function describeKeyError(error: unknown): string | undefined {
  return describe(error, keyMessages, "The TMDB token could not be changed.");
}

// One sentence a person can act on. Bloom keeps upstream details to itself,
// and so does this text.
function describe(
  error: unknown,
  messages: Messages,
  fallback: string,
): string | undefined {
  if (error === null || error === undefined) {
    return undefined;
  }
  if (!(error instanceof ApiError)) {
    return fallback;
  }
  if (error.status === 403 && error.code === CSRF_REJECTED) {
    return CROSS_SITE;
  }
  if (error.status !== undefined && error.status in messages) {
    return messages[error.status];
  }
  if (error.kind === "network") {
    return UNREACHABLE;
  }
  if (error.kind === "aborted") {
    return TIMED_OUT;
  }
  return fallback;
}
