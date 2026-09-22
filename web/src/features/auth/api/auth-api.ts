import type { ApiClient } from "../../../lib/api/http-client.js";
import {
  sessionResponseSchema,
  type LoginInput,
  type SessionResponse,
} from "./auth-schemas.js";

export class AuthApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  // Mutations are not cancellable: an abandoned sign-in must still settle so
  // the session cache never disagrees with the cookie the server set.
  login(input: LoginInput): Promise<SessionResponse> {
    return this.#client.requestJson(
      "api/v1/auth/login",
      sessionResponseSchema,
      { method: "POST", body: input },
    );
  }

  logout(): Promise<void> {
    return this.#client.requestEmpty("api/v1/auth/logout", {
      method: "POST",
    });
  }

  me(signal: AbortSignal): Promise<SessionResponse> {
    return this.#client.requestJson("api/v1/auth/me", sessionResponseSchema, {
      signal,
    });
  }
}
