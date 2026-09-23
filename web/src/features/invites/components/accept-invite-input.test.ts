import { expect, it } from "@jest/globals";
import { readAcceptInviteInput } from "./accept-invite-input.js";

function form(fields: Readonly<Record<string, string>>): FormData {
  const data = new FormData();
  for (const [name, value] of Object.entries(fields)) {
    data.set(name, value);
  }
  return data;
}

it("accepts a trimmed username and a long enough password", () => {
  const result = readAcceptInviteInput(
    form({ username: " alice ", password: "correct horse battery" }),
  );
  expect(result.input).toEqual({
    username: "alice",
    password: "correct horse battery",
  });
});

it("reports one message per field that needs attention", () => {
  const result = readAcceptInviteInput(
    form({ username: "", password: "short" }),
  );
  expect(result.errors).toEqual({
    username: "Choose a username.",
    password: "Use at least 15 characters.",
  });
});

it("rejects control characters and oversized usernames", () => {
  expect(
    readAcceptInviteInput(form({ username: "ab", password: "x".repeat(15) }))
      .errors,
  ).toEqual({ username: "The username must not contain control characters." });
  expect(
    readAcceptInviteInput(
      form({ username: "é".repeat(33), password: "x".repeat(15) }),
    ).errors,
  ).toEqual({ username: "Use a username of at most 64 bytes." });
});
