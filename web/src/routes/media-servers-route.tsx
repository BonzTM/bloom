import { useState, type ReactNode } from "react";
import { AsyncStatus } from "../components/async-status.js";
import {
  useSession,
  useSessionRecheck,
} from "../features/auth/hooks/auth-queries.js";
import type { RegisteredMediaServer } from "../features/media-servers/api/media-servers-schemas.js";
import { MediaServersTable } from "../features/media-servers/components/media-servers-table.js";
import { RegisterMediaServerForm } from "../features/media-servers/components/register-media-server-form.js";
import {
  useMediaServers,
  useRegisterMediaServer,
  useRemoveMediaServer,
} from "../features/media-servers/hooks/media-servers-queries.js";
import { accessDenial } from "../lib/api/errors.js";
import { AccessDeniedRoute } from "./access-denied-route.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

export default function MediaServersRoute(): ReactNode {
  usePageTitle(pageTitle("Media servers"));
  const session = useSession();
  const accountId = session.data?.account.id;
  // The route guard only renders this page for a signed-in account. Keying
  // the page on the account remounts it when the principal changes, so a
  // late answer to one account's action can never show under another's.
  if (accountId === undefined) {
    return null;
  }
  return <MediaServersPage key={accountId} accountId={accountId} />;
}

function MediaServersPage({
  accountId,
}: Readonly<{ accountId: string }>): ReactNode {
  const servers = useMediaServers(accountId);
  const register = useRegisterMediaServer(accountId);
  const remove = useRemoveMediaServer(accountId);
  const denial =
    accessDenial(servers.error) ??
    accessDenial(register.error) ??
    accessDenial(remove.error);
  // The server denied something the cached session says is allowed: the
  // session is gone, or a permission was taken away. Re-reading the session
  // lets the route guard send the person to sign in or off this page; if the
  // server then still reports the same session, the retry below stands.
  useSessionRecheck(
    denial !== undefined,
    Math.max(servers.errorUpdatedAt, register.submittedAt, remove.submittedAt),
  );
  if (denial === "forbidden") {
    return <AccessDeniedRoute />;
  }
  return (
    <>
      <h1>Media servers</h1>
      <p className="page-intro">
        Bloom manages users, statistics, and requests for the servers registered
        here. Registration checks the address and API key against the server
        before anything is saved.
      </p>
      <RegisterSection register={register} />
      <section aria-labelledby="registered-servers-heading" className="card">
        <h2 id="registered-servers-heading">Registered servers</h2>
        {denial === "unauthenticated" ? (
          <SignInNotConfirmed onRetry={servers.refetch} />
        ) : (
          <MediaServersTable
            query={servers}
            removing={remove.isPending ? remove.variables : undefined}
            removeError={remove.error}
            onRemove={(id) => {
              remove.mutate(id);
            }}
          />
        )}
      </section>
    </>
  );
}

function RegisterSection({
  register,
}: Readonly<{
  register: ReturnType<typeof useRegisterMediaServer>;
}>): ReactNode {
  const [registered, setRegistered] = useState<RegisteredMediaServer | null>(
    null,
  );
  // Remounting the form after a success clears the API key from the screen.
  const [formKey, setFormKey] = useState(0);
  return (
    <section aria-labelledby="register-server-heading" className="card">
      <h2 id="register-server-heading">Register a server</h2>
      <RegisterMediaServerForm
        key={formKey}
        pending={register.isPending}
        serverError={register.error}
        onSubmit={(input) => {
          setRegistered(null);
          register.register(input, {
            onSuccess: (result) => {
              setRegistered(result);
              setFormKey((current) => current + 1);
            },
          });
        }}
      />
      {registered === null ? null : (
        <AsyncStatus>
          Registered {registered.server.name}: {registered.info.name} version{" "}
          {registered.info.version}.
        </AsyncStatus>
      )}
    </section>
  );
}

function SignInNotConfirmed({
  onRetry,
}: Readonly<{ onRetry: () => Promise<unknown> }>): ReactNode {
  return (
    <>
      <AsyncStatus kind="alert">
        The media servers could not be loaded because your sign-in could not be
        confirmed.
      </AsyncStatus>
      <button
        type="button"
        onClick={() => {
          void onRetry();
        }}
      >
        Retry
      </button>
    </>
  );
}
