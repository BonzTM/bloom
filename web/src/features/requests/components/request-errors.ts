import { ApiError, CSRF_REJECTED } from "../../../lib/api/errors.js";

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
  404: "No TMDB key is stored.",
  422: "Enter a TMDB API key without control characters.",
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

export function describeSearchError(error: unknown): string | undefined {
  if (error instanceof ApiError && error.code === METADATA_NOT_CONFIGURED) {
    return "Searching needs a TMDB key, which no administrator has set yet.";
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
  502: "The instance could not be reached or refused the API key.",
  503: "The instance is busy. Try again in a moment.",
};

export function describeManagerError(error: unknown): string | undefined {
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
  return describe(error, keyMessages, "The TMDB key could not be changed.");
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
  return fallback;
}
