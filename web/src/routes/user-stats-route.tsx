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
import {
  BreakdownCharts,
  DailyCharts,
  ReportFailed,
  TitleCharts,
  TotalsCards,
} from "../features/playback/components/stats-panels.js";
import {
  formatActiveTime,
  itemTitle,
  playMethodLabel,
  whereLabel,
} from "../features/playback/components/watch-format.js";
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
  const username = user.data?.watches[0]?.username;
  usePageTitle(
    pageTitle(username === undefined || username === "" ? "Person" : username),
  );
  const denial = accessDenial(user.error);
  useSessionRecheck(denial !== undefined, user.errorUpdatedAt);
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
        <>
          <section aria-labelledby="user-totals-heading" className="card">
            <h2 id="user-totals-heading">Totals</h2>
            <TotalsCards totals={user.data.totals} />
            <TitleCharts titles={user.data.titles} />
            <BreakdownCharts
              clients={user.data.clients}
              devices={user.data.devices}
              playMethods={user.data.play_methods}
            />
          </section>
          <section aria-labelledby="user-daily-heading" className="card">
            <h2 id="user-daily-heading">Over time</h2>
            <DailyCharts items={user.data.daily} />
          </section>
          <section aria-labelledby="user-watches-heading" className="card">
            <h2 id="user-watches-heading">Latest watches</h2>
            {user.data.watches.length === 0 ? (
              <p>Nothing recorded in this window.</p>
            ) : (
              <div
                className="table-scroll"
                role="region"
                aria-label="Latest watches table"
                tabIndex={0}
              >
                <table>
                  <caption>Latest watches, newest first</caption>
                  <thead>
                    <tr>
                      <th scope="col">Title</th>
                      <th scope="col">Where</th>
                      <th scope="col">Method</th>
                      <th scope="col">Active</th>
                      <th scope="col">Started</th>
                    </tr>
                  </thead>
                  <tbody>
                    {user.data.watches.map((watch) => (
                      <tr key={watch.id}>
                        <th scope="row">{itemTitle(watch)}</th>
                        <td>{whereLabel(watch)}</td>
                        <td>{playMethodLabel(watch.play_method)}</td>
                        <td>{formatActiveTime(watch.active_seconds)}</td>
                        <td>
                          <time dateTime={watch.started_at}>
                            {watch.started_at.slice(0, 16).replace("T", " ")}
                          </time>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>
        </>
      )}
    </>
  );
}
