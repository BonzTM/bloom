import { useState, type ReactNode } from "react";
import { useLocation } from "react-router-dom";
import { AsyncStatus } from "../components/async-status.js";
import { LoginForm } from "../features/auth/components/login-form.js";
import { useLogin, useSession } from "../features/auth/hooks/auth-queries.js";
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
