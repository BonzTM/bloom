import { z } from "zod/v4";

// The backend's structured error envelope (internal/httputil.ErrorResponse).
// The body is trusted for display only when the whole envelope parses; any
// other shape, such as a proxy error page, degrades to a generic message so
// server internals never reach the screen.
const envelopeSchema = z.object({
  code: z.string().min(1).max(100),
  message: z.string().min(1).max(1000),
  request_id: z.string().max(200).optional(),
});

export type ApiErrorKind = "aborted" | "http" | "invalid-response" | "network";

type ApiErrorOptions = Readonly<{
  cause?: unknown;
  status?: number;
  code?: string;
  requestId?: string;
  retryAfterSeconds?: number;
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
  const options: {
    -readonly [K in keyof ApiErrorOptions]: ApiErrorOptions[K];
  } = {
    status,
  };
  const retryAfterSeconds = parseRetryAfter(retryAfterHeader);
  if (retryAfterSeconds !== undefined) {
    options.retryAfterSeconds = retryAfterSeconds;
  }
  if (!envelope.success) {
    return new ApiError(
      "http",
      `Request failed with status ${String(status)}`,
      options,
    );
  }
  options.code = envelope.data.code;
  if (envelope.data.request_id !== undefined) {
    options.requestId = envelope.data.request_id;
  }
  return new ApiError("http", envelope.data.message, options);
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
