import { useState, type ReactNode } from "react";
import { Navigate, useLocation, useNavigate } from "react-router-dom";
import { AsyncStatus } from "../components/async-status.js";
import { LoginForm } from "../features/auth/components/login-form.js";
import { useLogin, useSession } from "../features/auth/hooks/auth-queries.js";
import { safeDestination } from "./safe-destination.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

export default function LoginRoute(): ReactNode {
  usePageTitle(pageTitle("Sign in"));
  const session = useSession();
  const login = useLogin();
  const navigate = useNavigate();
  const location = useLocation();
  const [serverError, setServerError] = useState<unknown>(null);
  const destination = safeDestination(location.state, window.location.origin);

  if (session.isPending) {
    return <AsyncStatus>Checking sign-in…</AsyncStatus>;
  }
  // Already signed in: a declarative redirect, so Strict Mode's double
  // effects cannot navigate twice and a pending mutation is never cut short.
  if (session.isSuccess && session.data !== null && !login.isPending) {
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
            onSuccess: () => {
              void navigate(destination, { replace: true });
            },
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
