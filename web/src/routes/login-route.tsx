import { useState, type ReactNode } from "react";
import { Navigate, useLocation } from "react-router-dom";
import { AsyncStatus } from "../components/async-status.js";
import { LoginForm } from "../features/auth/components/login-form.js";
import { useLogin, useSession } from "../features/auth/hooks/auth-queries.js";
import { safeDestination } from "./safe-destination.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

export default function LoginRoute(): ReactNode {
  usePageTitle(pageTitle("Sign in"));
  const session = useSession();
  const login = useLogin();
  const location = useLocation();
  const [serverError, setServerError] = useState<unknown>(null);
  const destination = safeDestination(location.state, window.location.origin);

  if (session.data === undefined && !session.isError) {
    return <AsyncStatus>Checking sign-in…</AsyncStatus>;
  }
  // The session cache is the single owner of the redirect: a successful login
  // writes the account there, and this declarative redirect follows. Nothing
  // navigates imperatively, so Strict Mode cannot navigate twice.
  if (session.data !== undefined && session.data !== null && !login.isPending) {
    return <Navigate to={destination} replace />;
  }
  return (
    <>
      <h1>Sign in</h1>
      <LoginForm
        pending={login.isPending}
        serverError={serverError}
        onSubmit={(input) => {
          setServerError(null);
          login.mutate(input, {
            onError: setServerError,
            // Drop the variables (the password) as soon as the request settles.
            onSettled: () => {
              login.reset();
            },
          });
        }}
      />
    </>
  );
}
