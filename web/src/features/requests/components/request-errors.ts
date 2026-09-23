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
  422: "Check the profile fields and try again.",
};

const decisionMessages: Messages = {
  401: SIGN_IN_AGAIN,
  403: "You no longer have permission to decide requests.",
  404: "That request no longer exists.",
  409: "That request was already decided.",
  422: "Check the reason and try again.",
};

const keyMessages: Messages = {
  401: SIGN_IN_AGAIN,
  403: "You no longer have permission to change settings.",
  404: "No TMDB key is stored.",
  422: "Enter a TMDB API key without control characters.",
};

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
