import { ApiError } from "../../../lib/api/errors.js";

const GENERIC_FAILURE = "Sign-in failed. Please try again.";

const messagesByStatus: Readonly<Record<number, string>> = {
  401: "The username or password is incorrect.",
  422: "Check the username and password and try again.",
};

// Maps a failed sign-in into one sentence a person can act on. The server never
// says whether the username or the password was wrong, and neither do we.
export function describeLoginError(error: unknown): string | undefined {
  if (error === null || error === undefined) {
    return undefined;
  }
  if (!(error instanceof ApiError)) {
    return GENERIC_FAILURE;
  }
  if (error.status === 429) {
    return describeRateLimit(error.retryAfterSeconds);
  }
  if (error.status !== undefined && error.status in messagesByStatus) {
    return messagesByStatus[error.status];
  }
  if (error.kind === "network") {
    return "The server could not be reached. Check your connection and try again.";
  }
  return GENERIC_FAILURE;
}

function describeRateLimit(retryAfterSeconds: number | undefined): string {
  if (retryAfterSeconds === undefined || retryAfterSeconds < 1) {
    return "Too many sign-in attempts. Please wait a moment and try again.";
  }
  const unit = retryAfterSeconds === 1 ? "second" : "seconds";
  return `Too many sign-in attempts. Try again in ${String(retryAfterSeconds)} ${unit}.`;
}
