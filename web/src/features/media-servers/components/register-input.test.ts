import { expect, it } from "@jest/globals";
import { readRegisterInput } from "./register-input.js";

function form(fields: Readonly<Record<string, string>>): FormData {
  const data = new FormData();
  for (const [name, value] of Object.entries(fields)) {
    data.set(name, value);
  }
  return data;
}

it("accepts an HTTPS server and trims the name and address", () => {
  const result = readRegisterInput(
    form({
      name: "  Office ",
      base_url: " https://office.example/ ",
      api_key: "k3y",
    }),
  );
  expect(result.input).toEqual({
    kind: "jellyfin",
    name: "Office",
    base_url: "https://office.example/",
    api_key: "k3y",
    allow_insecure: false,
  });
});

it("accepts plaintext HTTP only with the override", () => {
  const withoutOverride = readRegisterInput(
    form({ name: "Cabin", base_url: "http://10.0.0.5:8096", api_key: "k3y" }),
  );
  expect(withoutOverride.errors).toEqual({
    base_url: expect.stringContaining("allow plaintext HTTP"),
  });

  const withOverride = readRegisterInput(
    form({
      name: "Cabin",
      base_url: "http://10.0.0.5:8096",
      api_key: "k3y",
      allow_insecure: "on",
    }),
  );
  expect(withOverride.input?.allow_insecure).toBe(true);
});

it("rejects the override for an HTTPS address", () => {
  const result = readRegisterInput(
    form({
      name: "Office",
      base_url: "https://office.example",
      api_key: "k3y",
      allow_insecure: "on",
    }),
  );
  expect(result.errors).toEqual({
    allow_insecure: "This override applies to http:// addresses only.",
  });
});

it("reports one message per empty or malformed field", () => {
  const result = readRegisterInput(
    form({ name: "", base_url: "ftp://media", api_key: "" }),
  );
  expect(result.errors).toEqual({
    name: "Enter a name for this server.",
    base_url: "Enter an http:// or https:// address.",
    api_key: "Enter the API key.",
  });
});

it("rejects an API key with control characters", () => {
  const result = readRegisterInput(
    form({ name: "Office", base_url: "https://office.example", api_key: "ab" }),
  );
  expect(result.errors).toEqual({
    api_key: "The API key must not contain control characters.",
  });
});

it("measures the name bound in UTF-8 bytes", () => {
  const result = readRegisterInput(
    form({
      name: "é".repeat(51),
      base_url: "https://office.example",
      api_key: "k3y",
    }),
  );
  expect(result.errors).toEqual({
    name: "Use a name of at most 100 bytes.",
  });
});
