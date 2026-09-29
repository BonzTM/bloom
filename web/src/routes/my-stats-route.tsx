import { useMemo, useState, type ReactNode } from "react";
import { AsyncStatus } from "../components/async-status.js";
import {
  useSession,
  useSessionRecheck,
} from "../features/auth/hooks/auth-queries.js";
import {
  MEDIA_USER_NOT_LINKED,
  type AccountMediaUser,
  type StatsParams,
} from "../features/playback/api/stats-schemas.js";
import {
  browserTimeZone,
  StatsControls,
  type WindowDays,
} from "../features/playback/components/stats-controls.js";
import { LinkMediaUserForm } from "../features/playback/components/link-media-user-form.js";
import { ReportFailed } from "../features/playback/components/stats-panels.js";
import { useMediaServers } from "../features/media-servers/hooks/media-servers-queries.js";
import { hasPermission, permissions } from "../features/auth/permissions.js";
import { UserDashboard } from "../features/playback/components/user-dashboard.js";
import {
  useMyMediaUsers,
  useStatsMe,
} from "../features/playback/hooks/stats-queries.js";
import { accessDenial, ApiError } from "../lib/api/errors.js";
import { AccessDeniedRoute } from "./access-denied-route.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

// The caller's own dashboard, through the media-server user linked to the
// account. Several links mean one dashboard per server, picked in the
// controls.
export default function MyStatsRoute(): ReactNode {
  usePageTitle(pageTitle("My statistics"));
  const session = useSession();
  const accountId = session.data?.account.id;
  const canLink =
    session.data !== undefined &&
    session.data !== null &&
    hasPermission(session.data.permissions, permissions.adminSettings);
  if (accountId === undefined) {
    return null;
  }
  return (
    <MyStatsPage key={accountId} accountId={accountId} canLink={canLink} />
  );
}

function MyStatsPage({
  accountId,
  canLink,
}: Readonly<{ accountId: string; canLink: boolean }>): ReactNode {
  const [days, setDays] = useState<WindowDays>(30);
  const [serverId, setServerId] = useState<string | undefined>(undefined);
  const timeZone = useMemo(() => browserTimeZone(), []);
  const params: StatsParams = useMemo(
    () => ({
      days,
      timeZone,
      ...(serverId === undefined ? {} : { mediaServerId: serverId }),
    }),
    [days, serverId, timeZone],
  );
  const links = useMyMediaUsers(accountId);
  const me = useStatsMe(accountId, params);
  const denial = accessDenial(me.error) ?? accessDenial(links.error);
  useSessionRecheck(
    denial !== undefined,
    Math.max(me.errorUpdatedAt, links.errorUpdatedAt),
  );
  if (denial === "forbidden") {
    return <AccessDeniedRoute />;
  }
  const items = links.data?.items ?? [];
  const notLinked =
    me.error instanceof ApiError && me.error.code === MEDIA_USER_NOT_LINKED;
  return (
    <>
      <h1>My statistics</h1>
      <p className="page-intro">
        What you watched and when, from the media-server user linked to your
        account.
      </p>
      <StatsControls
        days={days}
        onDaysChange={setDays}
        servers={items.map((link) => ({
          id: link.media_server_id,
          name: link.media_server_name,
        }))}
        serverId={serverId}
        onServerChange={setServerId}
        libraries={[]}
        library={undefined}
        onLibraryChange={() => undefined}
        timeZone={timeZone}
      />
      {denial === "unauthenticated" ? (
        <AsyncStatus kind="alert">
          Statistics could not be loaded because your sign-in could not be
          confirmed.
        </AsyncStatus>
      ) : notLinked ? (
        <NotLinked accountId={accountId} canLink={canLink} />
      ) : me.status === "pending" ? (
        <AsyncStatus>Loading your statistics…</AsyncStatus>
      ) : me.status === "error" ? (
        <ReportFailed what="Your statistics" onRetry={me.refetch} />
      ) : (
        <>
          <LinkedAs
            link={items.find(
              (link) => link.media_server_id === me.data.window.media_server_id,
            )}
            mediaUserId={me.data.window.media_user_id}
          />
          <UserDashboard data={me.data} />
        </>
      )}
    </>
  );
}

function LinkedAs({
  link,
  mediaUserId,
}: Readonly<{
  link: AccountMediaUser | undefined;
  mediaUserId: string | undefined;
}>): ReactNode {
  if (link === undefined) {
    return mediaUserId === undefined ? null : (
      <p className="row-detail">
        Showing <code>{mediaUserId}</code>.
      </p>
    );
  }
  return (
    <p className="row-detail">
      Showing {link.username} on {link.media_server_name}.
    </p>
  );
}

function NotLinked({
  accountId,
  canLink,
}: Readonly<{ accountId: string; canLink: boolean }>): ReactNode {
  const servers = useMediaServers(canLink ? accountId : undefined);
  const serverOptions = (servers.data?.pages ?? []).flatMap((page) =>
    page.items.map((server) => ({ id: server.id, name: server.name })),
  );
  return (
    <section aria-labelledby="not-linked-heading" className="card panel">
      <h2 id="not-linked-heading">No media-server user is linked yet</h2>
      <p>
        Bloom shows your statistics once your account is linked to a user on a
        media server. That happens when you accept an invite while signed in,
        when a media-server user has the same username as your account, or when
        an administrator links one for you.
      </p>
      {canLink ? (
        servers.status === "pending" ? (
          <AsyncStatus>Loading media servers…</AsyncStatus>
        ) : serverOptions.length === 0 ? (
          <p>Register a media server first, then link your account here.</p>
        ) : (
          <LinkMediaUserForm accountId={accountId} servers={serverOptions} />
        )
      ) : (
        <p>Ask an administrator to link your account.</p>
      )}
    </section>
  );
}
