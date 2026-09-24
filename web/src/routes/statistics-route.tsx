import { useMemo, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { AsyncStatus } from "../components/async-status.js";
import {
  useSession,
  useSessionRecheck,
} from "../features/auth/hooks/auth-queries.js";
import type { StatsParams } from "../features/playback/api/stats-schemas.js";
import {
  browserTimeZone,
  StatsControls,
  type LibraryFilter,
  type WindowDays,
} from "../features/playback/components/stats-controls.js";
import {
  BreakdownCharts,
  DailyCharts,
  LibrariesChart,
  PatternCharts,
  ReportFailed,
  TitleCharts,
  TotalsCards,
  UsersChart,
} from "../features/playback/components/stats-panels.js";
import { useMediaServers } from "../features/media-servers/hooks/media-servers-queries.js";
import {
  useStatsDaily,
  useStatsLibraries,
  useStatsOverview,
  useStatsPatterns,
} from "../features/playback/hooks/stats-queries.js";
import { hasPermission, permissions } from "../features/auth/permissions.js";
import { accessDenial } from "../lib/api/errors.js";
import { AccessDeniedRoute } from "./access-denied-route.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

export default function StatisticsRoute(): ReactNode {
  usePageTitle(pageTitle("Statistics"));
  const session = useSession();
  const accountId = session.data?.account.id;
  const canListServers =
    session.data !== undefined &&
    session.data !== null &&
    hasPermission(session.data.permissions, permissions.adminSettings);
  if (accountId === undefined) {
    return null;
  }
  return (
    <StatisticsPage
      key={accountId}
      accountId={accountId}
      canListServers={canListServers}
    />
  );
}

function StatisticsPage({
  accountId,
  canListServers,
}: Readonly<{ accountId: string; canListServers: boolean }>): ReactNode {
  const [days, setDays] = useState<WindowDays>(30);
  const [serverId, setServerId] = useState<string | undefined>(undefined);
  const [library, setLibrary] = useState<LibraryFilter | undefined>(undefined);
  const timeZone = useMemo(() => browserTimeZone(), []);
  const params: StatsParams = useMemo(
    () => ({
      days,
      timeZone,
      ...(library === undefined
        ? serverId === undefined
          ? {}
          : { mediaServerId: serverId }
        : { mediaServerId: library.serverId, libraryId: library.id }),
    }),
    [days, library, serverId, timeZone],
  );
  // The library ranking is never filtered by library: it is the list the
  // filter chooses from and shows how the chosen one compares.
  const libraryParams: StatsParams = useMemo(
    () => ({
      days,
      timeZone,
      ...(serverId === undefined ? {} : { mediaServerId: serverId }),
    }),
    [days, serverId, timeZone],
  );
  const overview = useStatsOverview(accountId, params);
  const daily = useStatsDaily(accountId, params);
  const patterns = useStatsPatterns(accountId, params);
  const libraries = useStatsLibraries(accountId, libraryParams);
  const servers = useMediaServers(canListServers ? accountId : undefined);
  const denial =
    accessDenial(overview.error) ??
    accessDenial(daily.error) ??
    accessDenial(patterns.error) ??
    accessDenial(libraries.error);
  useSessionRecheck(
    denial !== undefined,
    Math.max(
      overview.errorUpdatedAt,
      daily.errorUpdatedAt,
      patterns.errorUpdatedAt,
      libraries.errorUpdatedAt,
    ),
  );
  if (denial === "forbidden") {
    return <AccessDeniedRoute />;
  }
  const serverOptions = (servers.data?.pages ?? []).flatMap((page) =>
    page.items.map((s) => ({ id: s.id, name: s.name })),
  );
  const libraryChoices = (libraries.data?.items ?? [])
    .filter((item) => item.library_id !== "")
    .map((item) => ({
      serverId: item.media_server_id,
      id: item.library_id,
      name: item.library_name,
    }));
  return (
    <>
      <h1>Statistics</h1>
      <p className="page-intro">
        What people watch, who watches most, and when. A play is one recorded
        watch; watch time is the time a session was actually playing.{" "}
        <Link to="/admin/playback">See what is playing now.</Link>
      </p>
      <StatsControls
        days={days}
        onDaysChange={setDays}
        servers={serverOptions}
        serverId={serverId}
        onServerChange={(id) => {
          setServerId(id);
          setLibrary(undefined);
        }}
        libraries={libraryChoices}
        library={library}
        onLibraryChange={(next) => {
          setLibrary(next);
          if (next !== undefined) {
            setServerId(next.serverId);
          }
        }}
        timeZone={timeZone}
      />
      {denial === "unauthenticated" ? (
        <AsyncStatus kind="alert">
          Statistics could not be loaded because your sign-in could not be
          confirmed.
        </AsyncStatus>
      ) : (
        <>
          <section aria-labelledby="totals-heading" className="card">
            <h2 id="totals-heading">Totals</h2>
            {overview.status === "pending" ? (
              <AsyncStatus>Loading totals…</AsyncStatus>
            ) : overview.status === "error" ? (
              <ReportFailed what="Totals" onRetry={overview.refetch} />
            ) : (
              <>
                <TotalsCards totals={overview.data.totals} />
                <TitleCharts titles={overview.data.titles} />
                <UsersChart users={overview.data.users} />
                <BreakdownCharts
                  clients={overview.data.clients}
                  devices={overview.data.devices}
                  playMethods={overview.data.play_methods}
                />
              </>
            )}
          </section>
          <section aria-labelledby="libraries-heading" className="card">
            <h2 id="libraries-heading">Libraries</h2>
            {libraries.status === "pending" ? (
              <AsyncStatus>Loading libraries…</AsyncStatus>
            ) : libraries.status === "error" ? (
              <ReportFailed what="Libraries" onRetry={libraries.refetch} />
            ) : (
              <LibrariesChart libraries={libraries.data.items} />
            )}
          </section>
          <section aria-labelledby="daily-heading" className="card">
            <h2 id="daily-heading">Over time</h2>
            {daily.status === "pending" ? (
              <AsyncStatus>Loading the daily series…</AsyncStatus>
            ) : daily.status === "error" ? (
              <ReportFailed what="The daily series" onRetry={daily.refetch} />
            ) : (
              <DailyCharts items={daily.data.items} />
            )}
          </section>
          <section aria-labelledby="patterns-heading" className="card">
            <h2 id="patterns-heading">When people watch</h2>
            {patterns.status === "pending" ? (
              <AsyncStatus>Loading patterns…</AsyncStatus>
            ) : patterns.status === "error" ? (
              <ReportFailed what="Patterns" onRetry={patterns.refetch} />
            ) : (
              <PatternCharts patterns={patterns.data} />
            )}
          </section>
        </>
      )}
    </>
  );
}
