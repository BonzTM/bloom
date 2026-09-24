import { describe, expect, it } from "@jest/globals";
import type { NotificationChannel } from "../api/notification-schemas.js";
import { firstInvalidChannelField, readChannelInput } from "./channel-input.js";

function form(
  entries: Readonly<Record<string, string | readonly string[]>>,
): FormData {
  const data = new FormData();
  for (const [key, value] of Object.entries(entries)) {
    for (const item of typeof value === "string" ? [value] : value) {
      data.append(key, item);
    }
  }
  return data;
}

const stored: NotificationChannel = {
  id: "9a1b2c3d-0000-4000-8000-000000000001",
  kind: "webhook",
  name: "ops-hooks",
  enabled: true,
  subscriptions: ["approved"],
  subject_template: "",
  body_template: "",
  allow_insecure: false,
  allow_private: false,
  credentials: { url_set: true, shared_secret_set: true, password_set: false },
  consecutive_failures: 0,
  created_at: "2026-09-20T10:00:00Z",
  updated_at: "2026-09-20T10:00:00Z",
};

const webhookForm = {
  kind: "webhook",
  name: " ops ",
  enabled: "on",
  subscriptions: ["approved", "failed"],
  subject_template: "",
  body_template: "",
  url: "https://hooks.example/bloom",
  shared_secret: "s3cret",
};

describe("readChannelInput", () => {
  it("builds a webhook request and trims the name", () => {
    const result = readChannelInput(form(webhookForm), undefined);
    expect(result.input).toEqual({
      kind: "webhook",
      name: "ops",
      enabled: true,
      subscriptions: ["approved", "failed"],
      subject_template: "",
      body_template: "",
      webhook: {
        url: "https://hooks.example/bloom",
        shared_secret: "s3cret",
        allow_insecure: false,
        allow_private: false,
      },
    });
  });

  it("needs a kind before anything else", () => {
    const result = readChannelInput(form({ name: "x" }), undefined);
    expect(result.errors).toEqual({ kind: "Choose a channel kind." });
  });

  it("requires the secrets when registering a webhook", () => {
    const result = readChannelInput(
      form({ ...webhookForm, url: "", shared_secret: "" }),
      undefined,
    );
    expect(Object.keys(result.errors ?? {})).toEqual(["url", "shared_secret"]);
    expect(firstInvalidChannelField(result.errors ?? {})).toBe("url");
  });

  it("keeps stored secrets when the fields are blank on edit", () => {
    const result = readChannelInput(
      form({ ...webhookForm, url: "", shared_secret: "" }),
      stored,
    );
    expect(result.input?.webhook).toEqual({
      allow_insecure: false,
      allow_private: false,
    });
  });

  it("does not keep secrets across a kind change", () => {
    const result = readChannelInput(
      form({ ...webhookForm, kind: "discord", webhook_url: "" }),
      stored,
    );
    expect(result.errors).toEqual({
      webhook_url: "Enter the Discord webhook URL starting with https://.",
    });
  });

  it("applies the plaintext transport rule in both directions", () => {
    const plain = readChannelInput(
      form({ ...webhookForm, url: "http://hooks.example/bloom" }),
      undefined,
    );
    expect(plain.errors).toEqual({
      allow_insecure: "Plaintext HTTP needs the explicit override.",
    });
    const pointless = readChannelInput(
      form({ ...webhookForm, allow_insecure: "on" }),
      undefined,
    );
    expect(pointless.errors).toEqual({
      allow_insecure: "The override applies to http:// only.",
    });
  });

  it("requires at least one event", () => {
    const result = readChannelInput(
      form({ ...webhookForm, subscriptions: [] }),
      undefined,
    );
    expect(result.errors).toEqual({
      subscriptions: "Choose at least one event.",
    });
  });

  it("builds an email request from the comma-separated recipients", () => {
    const result = readChannelInput(
      form({
        kind: "email",
        name: "admins",
        subscriptions: ["failed"],
        subject_template: "Bloom: {{.Title}}",
        body_template: "",
        smtp_host: "smtp.example",
        smtp_port: " 465 ",
        tls_mode: "implicit",
        auth_mode: "login",
        username: "bloom",
        password: "pw",
        from_address: "bloom@example.com",
        from_name: "Bloom",
        recipients: "a@example.com, b@example.com,",
        allow_private: "on",
      }),
      undefined,
    );
    expect(result.input?.email).toEqual({
      smtp_host: "smtp.example",
      smtp_port: 465,
      tls_mode: "implicit",
      auth_mode: "login",
      username: "bloom",
      password: "pw",
      from_address: "bloom@example.com",
      from_name: "Bloom",
      recipients: ["a@example.com", "b@example.com"],
      allow_private: true,
    });
    expect(result.input?.enabled).toBe(false);
  });

  it("maps schema failures onto the field that caused them", () => {
    const result = readChannelInput(
      form({
        kind: "email",
        name: "admins",
        subscriptions: ["failed"],
        subject_template: "",
        body_template: "",
        smtp_host: "smtp.example",
        smtp_port: "70000",
        username: "bloom",
        password: "pw",
        from_address: "not-an-address",
        from_name: "",
        recipients: "a@example.com, a@example.com",
      }),
      undefined,
    );
    expect(result.errors).toEqual({
      smtp_port: "Enter a port from 1 to 65535.",
      from_address: "Enter a valid from address.",
      recipients:
        "Enter 1 to 32 distinct recipient addresses, comma-separated.",
    });
    expect(firstInvalidChannelField(result.errors ?? {})).toBe("smtp_port");
  });

  it("rejects control characters in the name", () => {
    const result = readChannelInput(
      form({ ...webhookForm, name: "ops" }),
      undefined,
    );
    expect(result.errors).toEqual({
      name: "Enter a name of at most 100 bytes.",
    });
  });
});
