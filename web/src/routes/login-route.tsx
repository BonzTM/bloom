import { useState, type ReactNode } from "react";
import { useLocation } from "react-router-dom";
import { AsyncStatus } from "../components/async-status.js";
import { useAuthApi } from "../features/auth/auth-context.js";
import { LoginForm } from "../features/auth/components/login-form.js";
import { describeOidcError } from "../features/auth/components/oidc-errors.js";
import { OidcSignIn } from "../features/auth/components/oidc-sign-in.js";
import {
  useLogin,
  useSession,
  useSignInProviders,
} from "../features/auth/hooks/auth-queries.js";
import { safeDestination } from "./safe-destination.js";
import { useRedirectOnce } from "./use-redirect-once.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

export default function LoginRoute(): ReactNode {
  usePageTitle(pageTitle("Sign in"));
  const session = useSession();
  const { login, isPending, reset } = useLogin();
  const location = useLocation();
  const [serverError, setServerError] = useState<unknown>(null);
  const destination = safeDestination(location.state, window.location.origin);
  // The session cache is the single owner of the redirect: a successful login
  // writes the account there and this effect follows. A pending sign-in is
  // never cut short by a stale cached account.
  const signedIn =
    session.data !== undefined && session.data !== null && !isPending;
  useRedirectOnce(signedIn, destination, { replace: true });

  if (session.data === undefined && !session.isError) {
    return <AsyncStatus>Checking sign-in…</AsyncStatus>;
  }
  if (signedIn) {
    return <AsyncStatus>Signed in, taking you back…</AsyncStatus>;
  }
  return (
    <>
      <h1>Sign in</h1>
      <OidcFailure search={location.search} />
      <SingleSignOn returnTo={destination} />
      <LoginForm
        pending={isPending}
        serverError={serverError}
        onSubmit={(input) => {
          setServerError(null);
          login(input, { onError: setServerError, onSettled: reset });
        }}
      />
    </>
  );
}

// The server sends a failed single sign-on back here with `?error=<code>`.
function OidcFailure({ search }: Readonly<{ search: string }>): ReactNode {
  const message = describeOidcError(new URLSearchParams(search).get("error"));
  if (message === undefined) {
    return null;
  }
  return <p role="alert">{message}</p>;
}

// Renders the single sign-on entry when the operator enabled one. A failure
// to load the provider list must never block password sign-in.
function SingleSignOn({ returnTo }: Readonly<{ returnTo: string }>): ReactNode {
  const api = useAuthApi();
  const providers = useSignInProviders();
  const oidc = providers.data?.providers.find((p) => p.id === "oidc");
  if (oidc === undefined) {
    return null;
  }
  return (
    <OidcSignIn
      provider={oidc}
      startUrl={api.oidcStartUrl()}
      returnTo={returnTo}
    />
  );
}
