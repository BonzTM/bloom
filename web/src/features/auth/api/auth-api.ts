import type { ApiClient } from "../../../lib/api/http-client.js";
import {
  authProvidersResponseSchema,
  sessionResponseSchema,
  type AuthProvidersResponse,
  type LoginInput,
  type Session,
} from "./auth-schemas.js";

export class AuthApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  // Mutations are not cancellable: an abandoned sign-in must still settle so
  // the session cache never disagrees with the cookie the server set.
  login(input: LoginInput): Promise<Session> {
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

  providers(signal: AbortSignal): Promise<AuthProvidersResponse> {
    return this.#client.requestJson(
      "api/v1/auth/providers",
      authProvidersResponseSchema,
      { signal },
    );
  }

  // The single sign-on start URL, posted to by a plain form. The return path
  // travels as a form field and the server validates it again.
  oidcStartUrl(): string {
    return this.#client.url("api/v1/auth/oidc/start").toString();
  }

  me(signal: AbortSignal): Promise<Session> {
    return this.#client.requestJson("api/v1/auth/me", sessionResponseSchema, {
      signal,
    });
  }
}
