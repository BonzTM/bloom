import type { z } from "zod/v4";
import {
  authModeSchema,
  channelRequestSchema,
  notificationEventTypeSchema,
  notificationKindSchema,
  notificationLimits,
  tlsModeSchema,
  type ChannelRequest,
  type NotificationChannel,
} from "../api/notification-schemas.js";

export type ChannelField =
  | "kind"
  | "name"
  | "subscriptions"
  | "subject_template"
  | "body_template"
  | "url"
  | "shared_secret"
  | "webhook_url"
  | "smtp_host"
  | "smtp_port"
  | "username"
  | "password"
  | "from_address"
  | "recipients"
  | "allow_insecure";

export type ChannelFieldErrors = Partial<Record<ChannelField, string>>;

export type ReadChannelResult =
  | Readonly<{ input: ChannelRequest; errors?: undefined }>
  | Readonly<{ input?: undefined; errors: ChannelFieldErrors }>;

export const CHANNEL_FIELD_ORDER: readonly ChannelField[] = [
  "kind",
  "name",
  "subscriptions",
  "url",
  "shared_secret",
  "webhook_url",
  "smtp_host",
  "smtp_port",
  "username",
  "password",
  "from_address",
  "recipients",
  "allow_insecure",
  "subject_template",
  "body_template",
];

const MESSAGES: Readonly<Record<ChannelField, string>> = {
  kind: "Choose a channel kind.",
  name: `Enter a name of at most ${String(notificationLimits.maxNameBytes)} bytes.`,
  subscriptions: "Choose at least one event.",
  subject_template: `Use at most ${String(notificationLimits.maxTemplateBytes)} bytes for the subject.`,
  body_template: `Use at most ${String(notificationLimits.maxTemplateBytes)} bytes for the body.`,
  url: "Enter the webhook address as a URL starting with http:// or https://.",
  shared_secret: "Enter the shared secret without control characters.",
  webhook_url: "Enter the Discord webhook URL starting with https://.",
  smtp_host: "Enter the SMTP host name.",
  smtp_port: "Enter a port from 1 to 65535.",
  username: "Enter the SMTP username.",
  password: "Enter the SMTP password without control characters.",
  from_address: "Enter a valid from address.",
  recipients: `Enter 1 to ${String(notificationLimits.maxRecipients)} distinct recipient addresses, comma-separated.`,
  allow_insecure: "Plaintext HTTP needs the explicit override.",
};

// Turns the submitted form into the request the server accepts, or into one
// message per field. `existing` is the channel being edited: its stored
// secrets are kept when the secret fields are left blank.
export function readChannelInput(
  data: FormData,
  existing: NotificationChannel | undefined,
): ReadChannelResult {
  const kind = notificationKindSchema.safeParse(text(data.get("kind")));
  if (!kind.success) {
    return { errors: { kind: MESSAGES.kind } };
  }
  const common = {
    kind: kind.data,
    name: text(data.get("name")).trim(),
    enabled: data.get("enabled") === "on",
    subscriptions: data
      .getAll("subscriptions")
      .filter((value): value is string => typeof value === "string")
      .filter((value) => notificationEventTypeSchema.safeParse(value).success),
    subject_template: text(data.get("subject_template")),
    body_template: text(data.get("body_template")),
  };
  const part = kindPart(kind.data, data, existing);
  if (part.errors !== undefined) {
    return { errors: part.errors };
  }
  const parsed = channelRequestSchema.safeParse({ ...common, ...part.value });
  if (!parsed.success) {
    return { errors: fieldErrors(parsed.error, kind.data) };
  }
  return { input: parsed.data };
}

type KindPart =
  | Readonly<{ value: Record<string, unknown>; errors?: undefined }>
  | Readonly<{ value?: undefined; errors: ChannelFieldErrors }>;

function kindPart(
  kind: ChannelRequest["kind"],
  data: FormData,
  existing: NotificationChannel | undefined,
): KindPart {
  const keepsSecrets = existing?.kind === kind;
  switch (kind) {
    case "webhook": {
      const url = text(data.get("url")).trim();
      const sharedSecret = text(data.get("shared_secret")).trim();
      const errors: ChannelFieldErrors = {};
      if (url === "" && !(keepsSecrets && existing.credentials.url_set)) {
        errors.url = MESSAGES.url;
      }
      if (
        sharedSecret === "" &&
        !(keepsSecrets && existing.credentials.shared_secret_set)
      ) {
        errors.shared_secret = MESSAGES.shared_secret;
      }
      const transport = transportError(
        url,
        data.get("allow_insecure") === "on",
      );
      if (transport !== undefined) {
        errors.allow_insecure = transport;
      }
      if (Object.keys(errors).length > 0) {
        return { errors };
      }
      return {
        value: {
          webhook: {
            ...(url === "" ? {} : { url }),
            ...(sharedSecret === "" ? {} : { shared_secret: sharedSecret }),
            allow_insecure: data.get("allow_insecure") === "on",
            allow_private: data.get("allow_private") === "on",
          },
        },
      };
    }
    case "discord": {
      const url = text(data.get("webhook_url")).trim();
      if (url === "" && !(keepsSecrets && existing.credentials.url_set)) {
        return { errors: { webhook_url: MESSAGES.webhook_url } };
      }
      const transport = transportError(
        url,
        data.get("allow_insecure") === "on",
      );
      if (transport !== undefined) {
        return { errors: { allow_insecure: transport } };
      }
      return {
        value: {
          discord: {
            ...(url === "" ? {} : { webhook_url: url }),
            allow_insecure: data.get("allow_insecure") === "on",
            allow_private: data.get("allow_private") === "on",
          },
        },
      };
    }
    case "email": {
      const password = text(data.get("password")).trim();
      if (
        password === "" &&
        !(keepsSecrets && existing.credentials.password_set)
      ) {
        return { errors: { password: MESSAGES.password } };
      }
      return {
        value: {
          email: {
            smtp_host: text(data.get("smtp_host")).trim(),
            smtp_port: port(text(data.get("smtp_port"))),
            tls_mode: tlsModeSchema
              .catch("starttls")
              .parse(text(data.get("tls_mode"))),
            auth_mode: authModeSchema
              .catch("plain")
              .parse(text(data.get("auth_mode"))),
            username: text(data.get("username")).trim(),
            ...(password === "" ? {} : { password }),
            from_address: text(data.get("from_address")).trim(),
            from_name: text(data.get("from_name")).trim(),
            recipients: text(data.get("recipients"))
              .split(",")
              .map((item) => item.trim())
              .filter((item) => item !== ""),
            allow_private: data.get("allow_private") === "on",
          },
        },
      };
    }
  }
}

// Plaintext HTTP needs the override; the override means nothing for HTTPS.
function transportError(
  url: string,
  allowInsecure: boolean,
): string | undefined {
  if (url === "") {
    return undefined;
  }
  const plaintext = url.toLowerCase().startsWith("http://");
  if (plaintext && !allowInsecure) {
    return MESSAGES.allow_insecure;
  }
  if (!plaintext && allowInsecure) {
    return "The override applies to http:// only.";
  }
  return undefined;
}

function port(raw: string): unknown {
  const trimmed = raw.trim();
  return /^[0-9]{1,5}$/.test(trimmed) ? Number(trimmed) : trimmed;
}

export function firstInvalidChannelField(
  errors: ChannelFieldErrors,
): ChannelField | undefined {
  return CHANNEL_FIELD_ORDER.find((field) => errors[field] !== undefined);
}

function text(value: FormDataEntryValue | null): string {
  return typeof value === "string" ? value : "";
}

// Zod paths are ["webhook","url"] or ["name"]; the last segment names the
// form field, except the whole-object refinements which point at the kind.
function fieldErrors(
  error: z.ZodError,
  kind: ChannelRequest["kind"],
): ChannelFieldErrors {
  const errors: ChannelFieldErrors = {};
  for (const issue of error.issues) {
    const last = issue.path[issue.path.length - 1];
    const field = isField(last)
      ? last
      : kind === "webhook"
        ? "url"
        : kind === "discord"
          ? "webhook_url"
          : "smtp_host";
    errors[field] ??= MESSAGES[field];
  }
  return errors;
}

function isField(value: unknown): value is ChannelField {
  return (
    typeof value === "string" &&
    (CHANNEL_FIELD_ORDER as readonly string[]).includes(value)
  );
}
