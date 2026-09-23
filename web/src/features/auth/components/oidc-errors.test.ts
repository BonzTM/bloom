import { expect, it } from "@jest/globals";
import { describeOidcError } from "./oidc-errors.js";

it("says nothing without an error code", () => {
  expect(describeOidcError(null)).toBeUndefined();
  expect(describeOidcError("")).toBeUndefined();
});

it.each([
  ["unknown_identity", "not linked to a Bloom account"],
  ["provisioning_disabled", "not linked to a Bloom account"],
  ["state_invalid", "expired or was interrupted"],
  ["token_invalid", "expired or was interrupted"],
  ["provider_unavailable", "could not be reached"],
  ["disabled", "This account is disabled."],
  ["internal_error", "Single sign-on failed."],
])("explains the %s code", (code, fragment) => {
  expect(describeOidcError(code)).toContain(fragment);
});

it.each([
  ["something_new"],
  ["<script>alert(1)</script>"],
  ["a".repeat(65)],
  ["Mixed_Case"],
  ["__proto__"],
  ["constructor"],
  ["hasownproperty"],
])("falls back to the generic sentence for %s", (code) => {
  expect(describeOidcError(code)).toBe(
    "Single sign-on failed. Please try again.",
  );
});
