import { expect, it } from "@jest/globals";
import { readCreateInviteInput } from "./create-invite-input.js";

const SERVER = "3d7f1a2b-0000-4000-8000-000000000001";
const NOW = new Date("2026-09-24T12:00:00Z");

function form(fields: Readonly<Record<string, string>>): FormData {
  const data = new FormData();
  for (const [name, value] of Object.entries(fields)) {
    data.set(name, value);
  }
  return data;
}

it("computes the expiry from the chosen period and trims the label", () => {
  const result = readCreateInviteInput(
    form({
      media_server_id: SERVER,
      label: " Friends ",
      expiry: "week",
      max_uses: "",
    }),
    NOW,
  );
  expect(result.input).toEqual({
    media_server_id: SERVER,
    label: "Friends",
    expires_at: "2026-10-01T12:00:00.000Z",
  });
});

it("omits the expiry for never and reads a use limit", () => {
  const result = readCreateInviteInput(
    form({
      media_server_id: SERVER,
      label: "Family",
      expiry: "never",
      max_uses: "5",
    }),
    NOW,
  );
  expect(result.input).toEqual({
    media_server_id: SERVER,
    label: "Family",
    max_uses: 5,
  });
});

it("reports one message per field that needs attention", () => {
  const result = readCreateInviteInput(
    form({
      media_server_id: "",
      label: "",
      expiry: "someday",
      max_uses: "abc",
    }),
    NOW,
  );
  expect(result.errors).toEqual({
    media_server_id: "Choose the server this invite is for.",
    label: "Enter a label so this invite can be told apart later.",
    expiry: "Choose when the invite expires.",
    max_uses: "Enter a whole number of uses, or leave it empty.",
  });
});

it("bounds the use limit and the label size", () => {
  expect(
    readCreateInviteInput(
      form({
        media_server_id: SERVER,
        label: "x",
        expiry: "day",
        max_uses: "0",
      }),
      NOW,
    ).errors,
  ).toEqual({
    max_uses: "Allow at least one use, or leave it empty for no limit.",
  });
  expect(
    readCreateInviteInput(
      form({
        media_server_id: SERVER,
        label: "x",
        expiry: "day",
        max_uses: "1001",
      }),
      NOW,
    ).errors,
  ).toEqual({ max_uses: "Allow at most 1000 uses." });
  expect(
    readCreateInviteInput(
      form({
        media_server_id: SERVER,
        label: "é".repeat(51),
        expiry: "day",
        max_uses: "",
      }),
      NOW,
    ).errors,
  ).toEqual({ label: "Use a label of at most 100 bytes." });
});
