import { useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { AsyncStatus } from "../components/async-status.js";
import {
  useSession,
  useSessionRecheck,
} from "../features/auth/hooks/auth-queries.js";
import type { RegisteredDownloadManager } from "../features/requests/api/download-manager-schemas.js";
import { DownloadManagersTable } from "../features/requests/components/download-managers-table.js";
import { SignInNotConfirmed } from "../features/requests/components/list-states.js";
import { RegisterDownloadManagerForm } from "../features/requests/components/register-download-manager-form.js";
import {
  useDownloadManagers,
  useRegisterDownloadManager,
  useRemoveDownloadManager,
} from "../features/requests/hooks/requests-queries.js";
import { accessDenial } from "../lib/api/errors.js";
import { AccessDeniedRoute } from "./access-denied-route.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

export default function DownloadManagersRoute(): ReactNode {
  usePageTitle(pageTitle("Download managers"));
  const session = useSession();
  const accountId = session.data?.account.id;
  // The route guard only renders this page for a signed-in account. Keying
  // the page on the account remounts it when the principal changes.
  if (accountId === undefined) {
    return null;
  }
  return <DownloadManagersPage key={accountId} accountId={accountId} />;
}

function DownloadManagersPage({
  accountId,
}: Readonly<{ accountId: string }>): ReactNode {
  const managers = useDownloadManagers(accountId);
  const register = useRegisterDownloadManager(accountId);
  const remove = useRemoveDownloadManager(accountId);
  const denial =
    accessDenial(managers.error) ??
    accessDenial(register.error) ??
    accessDenial(remove.error);
  useSessionRecheck(
    denial !== undefined,
    Math.max(managers.errorUpdatedAt, register.submittedAt, remove.submittedAt),
  );
  if (denial === "forbidden") {
    return <AccessDeniedRoute />;
  }
  return (
    <>
      <h1>Download managers</h1>
      <p className="page-intro">
        Approved requests are sent to a Radarr or Sonarr instance registered
        here, chosen by the request profile. Registration checks the address and
        API key against the instance before anything is saved.{" "}
        <Link to="/admin/request-profiles">Manage request profiles.</Link>
      </p>
      <RegisterSection register={register} />
      <section aria-labelledby="registered-managers-heading" className="card">
        <h2 id="registered-managers-heading">Registered instances</h2>
        {denial === "unauthenticated" ? (
          <SignInNotConfirmed
            noun="download managers"
            onRetry={managers.refetch}
          />
        ) : (
          <DownloadManagersTable
            query={managers}
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
  register: ReturnType<typeof useRegisterDownloadManager>;
}>): ReactNode {
  const [registered, setRegistered] =
    useState<RegisteredDownloadManager | null>(null);
  // Remounting the form after a success clears the API key from the screen.
  const [formKey, setFormKey] = useState(0);
  return (
    <section aria-labelledby="register-manager-heading" className="card">
      <h2 id="register-manager-heading">Register an instance</h2>
      <RegisterDownloadManagerForm
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
          Registered {registered.manager.name}: {registered.info.name} version{" "}
          {registered.info.version}.
        </AsyncStatus>
      )}
    </section>
  );
}
