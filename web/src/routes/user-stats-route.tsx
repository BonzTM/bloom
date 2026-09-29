import { useMemo, useState, type ReactNode } from "react";
import { Link, useParams } from "react-router-dom";
import { AsyncStatus } from "../components/async-status.js";
import {
  useSession,
  useSessionRecheck,
} from "../features/auth/hooks/auth-queries.js";
import { mediaServerIdSchema } from "../features/playback/api/playback-schemas.js";
import {
  mediaUserIdSchema,
  type StatsParams,
} from "../features/playback/api/stats-schemas.js";
import {
  browserTimeZone,
  StatsControls,
  type WindowDays,
} from "../features/playback/components/stats-controls.js";
import { ReportFailed } from "../features/playback/components/stats-panels.js";
import { UserDashboard } from "../features/playback/components/user-dashboard.js";
import {
  DEFAULT_TIMELINE_GAP,
  UserTimeline,
} from "../features/playback/components/user-timeline.js";
import { useTimeline } from "../features/playback/hooks/playback-queries.js";
import { useStatsUser } from "../features/playback/hooks/stats-queries.js";
import { accessDenial, ApiError } from "../lib/api/errors.js";
import { AccessDeniedRoute } from "./access-denied-route.js";
import { NotFoundRoute } from "./not-found-route.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

// One media-server user's dashboard.
export default function UserStatsRoute(): ReactNode {
  const { serverId = "", userId = "" } = useParams();
  const session = useSession();
  const accountId = session.data?.account.id;
  if (
    !mediaServerIdSchema.safeParse(serverId).success ||
    !mediaUserIdSchema.safeParse(userId).success
  ) {
    return <NotFoundRoute />;
  }
  if (accountId === undefined) {
    return null;
  }
  return (
    <UserStatsPage
      key={`${accountId}/${serverId}/${userId}`}
      accountId={accountId}
      serverId={serverId}
      userId={userId}
    />
  );
}

function UserStatsPage({
  accountId,
  serverId,
  userId,
}: Readonly<{
  accountId: string;
  serverId: string;
  userId: string;
}>): ReactNode {
  const [days, setDays] = useState<WindowDays>(30);
  const timeZone = useMemo(() => browserTimeZone(), []);
  const params: StatsParams = useMemo(
    () => ({ days, timeZone }),
    [days, timeZone],
  );
  const user = useStatsUser(accountId, params, serverId, userId);
  const [gap, setGap] = useState(DEFAULT_TIMELINE_GAP);
  const timeline = useTimeline(accountId, serverId, userId, gap);
  const username =
    user.data?.watches[0]?.username ??
    timeline.data?.pages[0]?.items[0]?.username;
  usePageTitle(
    pageTitle(username === undefined || username === "" ? "Person" : username),
  );
  const denial = accessDenial(user.error) ?? accessDenial(timeline.error);
  useSessionRecheck(
    denial !== undefined,
    Math.max(user.errorUpdatedAt, timeline.errorUpdatedAt),
  );
  if (denial === "forbidden") {
    return <AccessDeniedRoute />;
  }
  if (user.error instanceof ApiError && user.error.status === 404) {
    return <NotFoundRoute />;
  }
  return (
    <>
      <p>
        <Link to="/admin/statistics">Back to statistics</Link>
      </p>
      <h1>{username === undefined || username === "" ? "Person" : username}</h1>
      <p className="page-intro">
        <code>{userId}</code> on this media server.
      </p>
      <StatsControls
        days={days}
        onDaysChange={setDays}
        servers={[]}
        serverId={undefined}
        onServerChange={() => undefined}
        libraries={[]}
        library={undefined}
        onLibraryChange={() => undefined}
        timeZone={timeZone}
      />
      {user.status === "pending" ? (
        <AsyncStatus>Loading this person's statistics…</AsyncStatus>
      ) : user.status === "error" ? (
        denial === "unauthenticated" ? (
          <AsyncStatus kind="alert">
            Statistics could not be loaded because your sign-in could not be
            confirmed.
          </AsyncStatus>
        ) : (
          <ReportFailed
            what="This person's statistics"
            onRetry={user.refetch}
          />
        )
      ) : (
        <UserDashboard data={user.data} />
      )}
      <section aria-labelledby="user-timeline-heading" className="card">
        <h2 id="user-timeline-heading">Timeline</h2>
        <p className="section-intro">
          Everything this person watched, newest first, with repeat plays of the
          same title folded into one sitting.
        </p>
        <UserTimeline query={timeline} gapSeconds={gap} onGapChange={setGap} />
      </section>
    </>
  );
}
