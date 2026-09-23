// Sentences for the error code the server appends when a single sign-on
// attempt fails and sends the person back here. The server's codes are a
// closed set; anything else gets the generic sentence.
// A Map, not an object: an object lookup would resolve inherited names such
// as "__proto__" or "constructor" from the query string.
const NOT_LINKED =
  "Your sign-on account is not linked to a Bloom account yet. Ask an administrator to add you.";
const INTERRUPTED =
  "The sign-in attempt expired or was interrupted. Please try again.";

const messagesByCode: ReadonlyMap<string, string> = new Map([
  ["unknown_identity", NOT_LINKED],
  ["provisioning_disabled", NOT_LINKED],
  ["state_invalid", INTERRUPTED],
  ["token_invalid", INTERRUPTED],
  [
    "provider_unavailable",
    "The sign-on provider could not be reached. Please try again in a moment.",
  ],
  ["disabled", "This account is disabled."],
  ["internal_error", "Single sign-on failed. Please try again."],
]);

const GENERIC = "Single sign-on failed. Please try again.";
const MAX_CODE_LENGTH = 64;

export function describeOidcError(code: string | null): string | undefined {
  if (code === null || code.length === 0) {
    return undefined;
  }
  if (code.length > MAX_CODE_LENGTH || !/^[a-z_]+$/.test(code)) {
    return GENERIC;
  }
  return messagesByCode.get(code) ?? GENERIC;
}
