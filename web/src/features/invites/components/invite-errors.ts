import { ApiError, CSRF_REJECTED } from "../../../lib/api/errors.js";

const UNREACHABLE =
  "Bloom could not be reached. Check your connection and try again.";
const CROSS_SITE =
  "The request was refused as cross-site. Reload the page and try again.";
const SIGN_IN_AGAIN =
  "Your sign-in could not be confirmed. Sign in again and retry.";
const NO_PERMISSION = "You no longer have permission to manage invites.";
const INVITE_INVALID =
  "This invite link is not valid, has expired, or has already been used.";
const SERVER_FAILED =
  "The media server refused the request or did not answer. Nothing was created.";

type Messages = Readonly<Record<number, string>>;

const createMessages: Messages = {
  401: SIGN_IN_AGAIN,
  403: NO_PERMISSION,
  404: "That media server no longer exists.",
  422: "Check the label, expiry, and use limit and try again.",
  502: "The media server could not be reached, so the invite was not created.",
};

const revokeMessages: Messages = {
  401: SIGN_IN_AGAIN,
  403: NO_PERMISSION,
  404: "That invite was already gone.",
};

const previewMessages: Messages = {
  404: INVITE_INVALID,
};

const acceptMessages: Messages = {
  404: INVITE_INVALID,
  409: "That username is already taken on the server. Choose another.",
  422: "Check the username and password against the rules above.",
  502: SERVER_FAILED,
};

export function describeCreateInviteError(error: unknown): string | undefined {
  return describe(error, createMessages, "The invite could not be created.");
}

export function describeRevokeInviteError(error: unknown): string | undefined {
  return describe(error, revokeMessages, "The invite could not be revoked.");
}

export function describePreviewError(error: unknown): string | undefined {
  return describe(error, previewMessages, "The invite could not be checked.");
}

export function describeAcceptError(error: unknown): string | undefined {
  return describe(error, acceptMessages, "The account could not be created.");
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
  if (error.status === 429) {
    return describeWait("Too many attempts.", error.retryAfterSeconds);
  }
  if (error.status === 503) {
    return describeWait(
      "That server is busy right now.",
      error.retryAfterSeconds,
    );
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

function describeWait(
  lead: string,
  retryAfterSeconds: number | undefined,
): string {
  if (retryAfterSeconds === undefined || retryAfterSeconds < 1) {
    return `${lead} Please wait a moment and try again.`;
  }
  const unit = retryAfterSeconds === 1 ? "second" : "seconds";
  return `${lead} Try again in ${String(retryAfterSeconds)} ${unit}.`;
}
