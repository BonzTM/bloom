import type { output, ZodType } from "zod/v4";
import { ApiError, mapHttpError } from "./errors.js";

const MAX_RESPONSE_BYTES = 1_000_000;
const DEFAULT_TIMEOUT_MS = 10_000;

type RequestOptions = Readonly<{
  method?: "GET" | "POST" | "DELETE";
  body?: unknown;
  headers?: Readonly<Record<string, string>>;
  signal?: AbortSignal;
}>;

export class ApiClient {
  readonly #baseUrl: URL;
  readonly #timeoutMs: number;

  constructor(baseUrl: URL, timeoutMs = DEFAULT_TIMEOUT_MS) {
    if (baseUrl.protocol !== "http:" && baseUrl.protocol !== "https:") {
      throw new TypeError("API base URL must use HTTP or HTTPS");
    }
    if (!Number.isInteger(timeoutMs) || timeoutMs < 1) {
      throw new TypeError("API timeout must be a positive integer");
    }
    this.#baseUrl = normalizeBaseUrl(baseUrl);
    this.#timeoutMs = timeoutMs;
  }

  async requestJson<S extends ZodType>(
    path: string,
    schema: S,
    options: RequestOptions = {},
  ): Promise<output<S>> {
    const response = await this.#request(path, options);
    assertJsonContentType(response);
    const text = await readBoundedBody(response);
    try {
      const body: unknown = JSON.parse(text);
      return schema.parse(body);
    } catch (error: unknown) {
      throw new ApiError(
        "invalid-response",
        "The server response was invalid",
        {
          cause: error,
        },
      );
    }
  }

  async requestEmpty(path: string, options: RequestOptions): Promise<void> {
    const response = await this.#request(path, options);
    const text = await readBoundedBody(response);
    if (text.length !== 0) {
      throw new ApiError(
        "invalid-response",
        "Expected an empty server response",
      );
    }
  }

  async #request(path: string, options: RequestOptions): Promise<Response> {
    const timeoutSignal = AbortSignal.timeout(this.#timeoutMs);
    const signal = combineSignals(options.signal, timeoutSignal);
    try {
      const requestInit: RequestInit = {
        method: options.method ?? "GET",
        headers: buildHeaders(options),
        signal,
      };
      if (options.body !== undefined) {
        requestInit.body = JSON.stringify(options.body);
      }
      const response = await fetch(new URL(path, this.#baseUrl), requestInit);
      if (!response.ok) {
        throw await readHttpError(response);
      }
      return response;
    } catch (error: unknown) {
      if (error instanceof ApiError) {
        throw error;
      }
      if (signal.aborted) {
        throw new ApiError("aborted", "The request was cancelled", {
          cause: error,
        });
      }
      throw new ApiError("network", "The server could not be reached", {
        cause: error,
      });
    }
  }
}

function combineSignals(
  callerSignal: AbortSignal | undefined,
  timeoutSignal: AbortSignal,
): AbortSignal {
  return callerSignal === undefined
    ? timeoutSignal
    : AbortSignal.any([callerSignal, timeoutSignal]);
}

function buildHeaders(options: RequestOptions): Headers {
  const headers = new Headers(options.headers);
  headers.set("accept", "application/json");
  if (options.body !== undefined) {
    headers.set("content-type", "application/json");
  }
  return headers;
}

function normalizeBaseUrl(baseUrl: URL): URL {
  const normalized = new URL(baseUrl);
  if (!normalized.pathname.endsWith("/")) {
    normalized.pathname = `${normalized.pathname}/`;
  }
  return normalized;
}

function assertJsonContentType(response: Response): void {
  const contentType = response.headers.get("content-type");
  if (contentType?.toLowerCase().includes("application/json") !== true) {
    throw new ApiError("invalid-response", "The server response was not JSON");
  }
}

async function readBoundedBody(response: Response): Promise<string> {
  const contentLength = response.headers.get("content-length");
  if (contentLength !== null && Number(contentLength) > MAX_RESPONSE_BYTES) {
    throw new ApiError("invalid-response", "The server response was too large");
  }
  const text = await response.text();
  if (new TextEncoder().encode(text).byteLength > MAX_RESPONSE_BYTES) {
    throw new ApiError("invalid-response", "The server response was too large");
  }
  return text;
}

async function readHttpError(response: Response): Promise<ApiError> {
  const text = await readBoundedBody(response);
  try {
    const body: unknown = JSON.parse(text);
    return mapHttpError(response.status, body);
  } catch {
    return mapHttpError(response.status, undefined);
  }
}
