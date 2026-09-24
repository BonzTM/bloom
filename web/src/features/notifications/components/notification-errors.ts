import { ApiError, CSRF_REJECTED } from "../../../lib/api/errors.js";

const NOTIFICATION_CHANNEL_FAILURE = "notification_channel_failure";

const messages: Readonly<Record<number, string>> = {
  401: "Your sign-in could not be confirmed. Sign in again and retry.",
  403: "You no longer have permission to manage notification channels.",
  404: "That channel no longer exists.",
  409: "Another channel already uses that name.",
  422: "Check the channel fields and try again.",
  502: "The channel could not be reached or refused the message.",
  503: "The channel is not answering. Try again in a moment.",
};

export function describeChannelError(error: unknown): string | undefined {
  if (error === null || error === undefined) {
    return undefined;
  }
  if (!(error instanceof ApiError)) {
    return "The channel could not be changed.";
  }
  if (error.status === 403 && error.code === CSRF_REJECTED) {
    return "The request was refused as cross-site. Reload the page and try again.";
  }
  if (error.code === NOTIFICATION_CHANNEL_FAILURE) {
    return messages[502];
  }
  if (error.status !== undefined && error.status in messages) {
    return messages[error.status];
  }
  if (error.kind === "network") {
    return "Bloom could not be reached. Check your connection and try again.";
  }
  return "The channel could not be changed.";
}
