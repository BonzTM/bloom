import type { ReactNode } from "react";
import { Link, useParams } from "react-router-dom";
import { AsyncStatus } from "../components/async-status.js";
import {
  useSession,
  useSessionRecheck,
} from "../features/auth/hooks/auth-queries.js";
import { hasPermission, permissions } from "../features/auth/permissions.js";
import { useLibraries } from "../features/catalog/hooks/catalog-queries.js";
import { mediaServerIdSchema } from "../features/media-servers/api/media-servers-schemas.js";
import {
  ExclusionsForm,
  type Choice,
} from "../features/media-servers/components/exclusions-form.js";
import {
  useExclusions,
  useMediaServers,
  useReplaceExclusions,
} from "../features/media-servers/hooks/media-servers-queries.js";
import { useMediaServerUsers } from "../features/playback/hooks/stats-queries.js";
import { LoadFailed } from "../features/requests/components/list-states.js";
import { accessDenial, ApiError } from "../lib/api/errors.js";
import { AccessDeniedRoute } from "./access-denied-route.js";
import { NotFoundRoute } from "./not-found-route.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

// Which people and libraries on one server Bloom leaves out.
export default function ExclusionsRoute(): ReactNode {
  const { serverId = "" } = useParams();
  const session = useSession();
  const accountId = session.data?.account.id;
  if (!mediaServerIdSchema.safeParse(serverId).success) {
    return <NotFoundRoute />;
  }
  if (accountId === undefined) {
    return null;
  }
  return (
    <ExclusionsPage
      key={`${accountId}/${serverId}`}
      accountId={accountId}
      serverId={serverId}
      canReadStats={hasPermission(
        session.data?.permissions ?? [],
        permissions.statsReadAll,
      )}
    />
  );
}

function ExclusionsPage({
  accountId,
  serverId,
  canReadStats,
}: Readonly<{
  accountId: string;
  serverId: string;
  canReadStats: boolean;
}>): ReactNode {
  const servers = useMediaServers(accountId);
  const exclusions = useExclusions(accountId, serverId);
  const replace = useReplaceExclusions(accountId, serverId);
  // The name lists need stats.read.all; without it the ids stand alone.
  const users = useMediaServerUsers(
    accountId,
    canReadStats ? serverId : undefined,
  );
  const libraries = useLibraries(
    accountId,
    canReadStats ? serverId : undefined,
  );
  const server = servers.data?.pages
    .flatMap((page) => page.items)
    .find((candidate) => candidate.id === serverId);
  usePageTitle(pageTitle(`Exclusions${server ? ` · ${server.name}` : ""}`));
  const denial = accessDenial(exclusions.error) ?? accessDenial(replace.error);
  useSessionRecheck(
    denial !== undefined,
    Math.max(exclusions.errorUpdatedAt, replace.submittedAt),
  );
  if (denial === "forbidden") {
    return <AccessDeniedRoute />;
  }
  if (exclusions.error instanceof ApiError && exclusions.error.status === 404) {
    return <NotFoundRoute />;
  }
  return (
    <>
      <p>
        <Link to="/admin/media-servers">Back to media servers</Link>
      </p>
      <h1>Exclusions</h1>
      <p className="page-intro">
        {server === undefined ? "This server" : server.name}: the people and
        libraries Bloom leaves out of collection, imports, statistics, and the
        catalog.
      </p>
      <section aria-labelledby="exclusions-heading" className="card">
        <h2 id="exclusions-heading">What is left out</h2>
        {exclusions.status === "pending" ? (
          <AsyncStatus>Loading exclusions…</AsyncStatus>
        ) : exclusions.status === "error" ? (
          denial === "unauthenticated" ? (
            <AsyncStatus kind="alert">
              Exclusions could not be loaded because your sign-in could not be
              confirmed.
            </AsyncStatus>
          ) : (
            <LoadFailed noun="exclusions" onRetry={exclusions.refetch} />
          )
        ) : (
          <ExclusionsForm
            key={JSON.stringify(exclusions.data)}
            stored={exclusions.data}
            users={(users.data?.items ?? []).map(toChoice)}
            libraries={(libraries.data ?? []).map(toChoice)}
            pending={replace.isPending}
            saved={replace.isSuccess}
            errorSummary={describeReplaceError(replace.error)}
            onSubmit={(input) => {
              replace.mutate(input);
            }}
          />
        )}
      </section>
    </>
  );
}

function toChoice(value: Readonly<{ id: string; name: string }>): Choice {
  return { id: value.id, name: value.name };
}

function describeReplaceError(error: unknown): string | undefined {
  if (error === null || error === undefined) {
    return undefined;
  }
  if (error instanceof ApiError && error.status === 422) {
    return "The server refused these exclusions; check the ids and try again.";
  }
  return "The exclusions could not be saved. Please try again.";
}
