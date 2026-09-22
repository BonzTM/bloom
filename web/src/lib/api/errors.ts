import { z } from "zod/v4";

const problemSchema = z.object({
  type: z.string(),
  title: z.string(),
  status: z.int(),
  detail: z.string().optional(),
  code: z.string().optional(),
});

export type ApiErrorKind = "aborted" | "http" | "invalid-response" | "network";

export class ApiError extends Error {
  readonly kind: ApiErrorKind;
  readonly status: number | undefined;

  constructor(
    kind: ApiErrorKind,
    message: string,
    options: Readonly<{ cause?: unknown; status?: number }> = {},
  ) {
    super(message, { cause: options.cause });
    this.name = "ApiError";
    this.kind = kind;
    this.status = options.status;
  }
}

export function mapHttpError(status: number, body: unknown): ApiError {
  const problem = problemSchema.safeParse(body);
  const message = problem.success
    ? (problem.data.detail ?? problem.data.title)
    : `Request failed with status ${String(status)}`;
  return new ApiError("http", message, { status });
}
