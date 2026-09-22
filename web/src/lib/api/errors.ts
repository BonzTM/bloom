import { z } from "zod/v4";

// The backend's structured error envelope (internal/httputil.ErrorResponse).
// Every field is optional here because a proxy or a crash can still produce a
// non-envelope body, and the client must degrade to a safe message.
const envelopeSchema = z.object({
  code: z.string().max(100).optional(),
  message: z.string().max(1000).optional(),
  request_id: z.string().max(200).optional(),
});

export type ApiErrorKind = "aborted" | "http" | "invalid-response" | "network";

type ApiErrorOptions = Readonly<{
  cause?: unknown;
  status?: number | undefined;
  code?: string | undefined;
  requestId?: string | undefined;
  retryAfterSeconds?: number | undefined;
}>;

export class ApiError extends Error {
  readonly kind: ApiErrorKind;
  readonly status: number | undefined;
  readonly code: string | undefined;
  readonly requestId: string | undefined;
  readonly retryAfterSeconds: number | undefined;

  constructor(
    kind: ApiErrorKind,
    message: string,
    options: ApiErrorOptions = {},
  ) {
    super(message, { cause: options.cause });
    this.name = "ApiError";
    this.kind = kind;
    this.status = options.status;
    this.code = options.code;
    this.requestId = options.requestId;
    this.retryAfterSeconds = options.retryAfterSeconds;
  }
}

export function mapHttpError(
  status: number,
  body: unknown,
  retryAfterHeader: string | null = null,
): ApiError {
  const envelope = envelopeSchema.safeParse(body);
  const data = envelope.success ? envelope.data : {};
  const message =
    data.message ?? `Request failed with status ${String(status)}`;
  return new ApiError("http", message, {
    status,
    code: data.code,
    requestId: data.request_id,
    retryAfterSeconds: parseRetryAfter(retryAfterHeader),
  });
}

// Only the delay-seconds form of Retry-After is honoured; the HTTP-date form
// is rare from our own backend and ignoring it degrades to "try again later".
export function parseRetryAfter(header: string | null): number | undefined {
  if (header === null) {
    return undefined;
  }
  const seconds = Number(header.trim());
  if (!Number.isInteger(seconds) || seconds < 0) {
    return undefined;
  }
  return seconds;
}
