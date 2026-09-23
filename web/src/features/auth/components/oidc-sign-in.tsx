import type { ReactNode } from "react";
import type { SignInProvider } from "../api/auth-schemas.js";

type OidcSignInProps = Readonly<{
  provider: SignInProvider;
  startUrl: string;
  returnTo: string;
}>;

// A plain form post, not a fetch: starting the flow changes the server-side
// session, so it is a POST (which the server's cross-origin protection
// covers), and the response is a full-page redirect to the identity provider.
export function OidcSignIn({
  provider,
  startUrl,
  returnTo,
}: OidcSignInProps): ReactNode {
  return (
    <section aria-labelledby="oidc-sign-in-heading" className="oidc-sign-in">
      <h2 id="oidc-sign-in-heading">Single sign-on</h2>
      <form method="post" action={startUrl}>
        <input type="hidden" name="return_to" value={returnTo} />
        <button type="submit" className="btn-primary">
          Continue with {provider.display_name}
        </button>
      </form>
      <p className="oidc-sign-in-or" aria-hidden="true">
        or sign in with a password
      </p>
    </section>
  );
}
