import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { AsyncStatus } from "../../../components/async-status.js";
import { BarChart } from "../../../components/charts/bar-chart.js";
import {
  formatCount,
  formatDuration,
  type ChartPoint,
} from "../../../components/charts/chart-data.js";
import { LineChart } from "../../../components/charts/line-chart.js";
import type {
  StatsBreakdown,
  StatsDailyBucket,
  StatsLibrary,
  StatsPatterns,
  StatsTitle,
  StatsTotals,
  StatsUser,
} from "../api/stats-schemas.js";
import { playMethodLabel } from "./watch-format.js";

const WEEKDAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

export function TotalsCards({
  totals,
}: Readonly<{ totals: StatsTotals }>): ReactNode {
  const cards = [
    { label: "Plays", value: formatCount(totals.plays) },
    { label: "Watch time", value: formatDuration(totals.watch_seconds) },
    { label: "People", value: formatCount(totals.unique_users) },
    { label: "Titles", value: formatCount(totals.unique_titles) },
  ];
  return (
    <ul className="stat-cards" aria-label="Totals">
      {cards.map((card) => (
        <li key={card.label} className="stat-card">
          <span className="stat-card-label">{card.label}</span>
          <strong className="stat-card-value">{card.value}</strong>
        </li>
      ))}
    </ul>
  );
}

export function DailyCharts({
  items,
}: Readonly<{ items: readonly StatsDailyBucket[] }>): ReactNode {
  const plays: ChartPoint[] = items.map((b) => ({
    label: b.date,
    value: b.plays,
  }));
  const time: ChartPoint[] = items.map((b) => ({
    label: b.date,
    value: b.watch_seconds,
  }));
  return (
    <div className="chart-grid">
      <LineChart
        title="Plays per day"
        labelHeading="Day"
        valueHeading="Plays"
        points={plays}
      />
      <LineChart
        title="Watch time per day"
        labelHeading="Day"
        valueHeading="Watch time"
        points={time}
        format={formatDuration}
      />
    </div>
  );
}

export function PatternCharts({
  patterns,
}: Readonly<{ patterns: StatsPatterns }>): ReactNode {
  return (
    <div className="chart-grid">
      <BarChart
        title="Plays by weekday"
        labelHeading="Weekday"
        valueHeading="Plays"
        orientation="vertical"
        points={patterns.weekdays.map((b) => ({
          label: WEEKDAYS[b.weekday] ?? String(b.weekday),
          value: b.plays,
        }))}
      />
      <BarChart
        title="Plays by hour"
        labelHeading="Hour"
        valueHeading="Plays"
        orientation="vertical"
        points={patterns.hours.map((b) => ({
          label: `${String(b.hour).padStart(2, "0")}:00`,
          value: b.plays,
        }))}
      />
    </div>
  );
}

export function TitleCharts({
  titles,
}: Readonly<{ titles: readonly StatsTitle[] }>): ReactNode {
  const byKind = (kind: StatsTitle["kind"]): ChartPoint[] =>
    titles
      .filter((t) => t.kind === kind)
      .map((t) => ({ label: t.name, value: t.plays }));
  return (
    <div className="chart-grid">
      <BarChart
        title="Most watched movies"
        labelHeading="Movie"
        valueHeading="Plays"
        points={byKind("movie")}
      />
      <BarChart
        title="Most watched series"
        labelHeading="Series"
        valueHeading="Plays"
        points={byKind("series")}
      />
    </div>
  );
}

// Users link to their own page; the server id is part of the address
// because a media user exists per server.
export function UsersChart({
  users,
}: Readonly<{ users: readonly StatsUser[] }>): ReactNode {
  return (
    <>
      <BarChart
        title="Most active people"
        labelHeading="Person"
        valueHeading="Watch time"
        points={users.map((u) => ({
          label: u.username || u.media_user_id,
          value: u.watch_seconds,
        }))}
        format={formatDuration}
      />
      {users.length === 0 ? null : (
        <ul className="user-links" aria-label="People with statistics">
          {users.map((u) => (
            <li key={`${u.media_server_id}/${u.media_user_id}`}>
              <Link to={userStatsPath(u.media_server_id, u.media_user_id)}>
                {u.username || u.media_user_id}
              </Link>
            </li>
          ))}
        </ul>
      )}
    </>
  );
}

export const UNKNOWN_LIBRARY = "Unknown library";

// Libraries ranked by plays. Watches recorded before their library was
// known are grouped under one unknown row.
export function LibrariesChart({
  libraries,
}: Readonly<{ libraries: readonly StatsLibrary[] }>): ReactNode {
  return (
    <BarChart
      title="Most watched libraries"
      labelHeading="Library"
      valueHeading="Plays"
      points={libraries.map((library) => ({
        label:
          library.library_id === "" ? UNKNOWN_LIBRARY : library.library_name,
        value: library.plays,
      }))}
      format={formatCount}
    />
  );
}

export function userStatsPath(
  mediaServerId: string,
  mediaUserId: string,
): string {
  return `/admin/statistics/users/${encodeURIComponent(mediaServerId)}/${encodeURIComponent(mediaUserId)}`;
}

export function BreakdownCharts({
  clients,
  devices,
  playMethods,
}: Readonly<{
  clients: readonly StatsBreakdown[];
  devices: readonly StatsBreakdown[];
  playMethods: readonly StatsBreakdown[];
}>): ReactNode {
  const points = (
    rows: readonly StatsBreakdown[],
    label = (n: string) => n,
  ): ChartPoint[] =>
    rows.map((r) => ({ label: label(r.name), value: r.plays }));
  return (
    <div className="chart-grid chart-grid-3">
      <BarChart
        title="Clients"
        labelHeading="Client"
        valueHeading="Plays"
        points={points(clients)}
      />
      <BarChart
        title="Devices"
        labelHeading="Device"
        valueHeading="Plays"
        points={points(devices)}
      />
      <BarChart
        title="Play methods"
        labelHeading="Method"
        valueHeading="Plays"
        points={points(playMethods, (name) => methodLabel(name))}
      />
    </div>
  );
}

function methodLabel(name: string): string {
  switch (name) {
    case "direct_play":
    case "direct_stream":
    case "transcode":
    case "unknown":
      return playMethodLabel(name);
    default:
      return name;
  }
}

export function ReportFailed({
  what,
  onRetry,
}: Readonly<{ what: string; onRetry: () => Promise<unknown> }>): ReactNode {
  return (
    <>
      <AsyncStatus kind="alert">{what} could not be loaded.</AsyncStatus>
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
