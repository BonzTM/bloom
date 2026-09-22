import { useEffect, type ReactNode } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { AsyncStatus } from "../components/async-status.js";
import { LoginForm } from "../features/auth/components/login-form.js";
import { useLogin, useSession } from "../features/auth/hooks/auth-queries.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

// Where to go after signing in: the page that redirected here, or home.
function destinationFrom(state: unknown): string {
  if (typeof state === "object" && state !== null && "from" in state) {
    const from: unknown = state.from;
    if (
      typeof from === "string" &&
      from.startsWith("/") &&
      !from.startsWith("//")
    ) {
      return from;
    }
  }
  return "/";
}

export default function LoginRoute(): ReactNode {
  usePageTitle(pageTitle("Sign in"));
  const session = useSession();
  const login = useLogin();
  const navigate = useNavigate();
  const location = useLocation();
  const destination = destinationFrom(location.state);
  const signedIn = session.data !== null && session.data !== undefined;

  useEffect(() => {
    if (signedIn) {
      void navigate(destination, { replace: true });
    }
  }, [signedIn, destination, navigate]);

  if (session.isPending) {
    return <AsyncStatus>Checking sign-in…</AsyncStatus>;
  }
  return (
    <>
      <h1>Sign in</h1>
      <LoginForm
        pending={login.isPending}
        serverError={login.error}
        onSubmit={(input) => {
          login.mutate(input);
        }}
      />
    </>
  );
}
