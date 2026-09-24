import type { ReactNode } from "react";
import type { StatsUserDetail } from "../api/stats-schemas.js";
import {
  BreakdownCharts,
  DailyCharts,
  TitleCharts,
  TotalsCards,
} from "./stats-panels.js";
import {
  formatActiveTime,
  itemTitle,
  playMethodLabel,
  whereLabel,
} from "./watch-format.js";

// One media-server user's dashboard: totals, rankings, breakdowns, the
// daily series, and the latest watches. Shared by the administrator's
// per-person page and the caller's own page.
export function UserDashboard({
  data,
}: Readonly<{ data: StatsUserDetail }>): ReactNode {
  return (
    <>
      <section aria-labelledby="user-totals-heading" className="card">
        <h2 id="user-totals-heading">Totals</h2>
        <TotalsCards totals={data.totals} />
        <TitleCharts titles={data.titles} />
        <BreakdownCharts
          clients={data.clients}
          devices={data.devices}
          playMethods={data.play_methods}
        />
      </section>
      <section aria-labelledby="user-daily-heading" className="card">
        <h2 id="user-daily-heading">Over time</h2>
        <DailyCharts items={data.daily} />
      </section>
      <section aria-labelledby="user-watches-heading" className="card">
        <h2 id="user-watches-heading">Latest watches</h2>
        {data.watches.length === 0 ? (
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
                {data.watches.map((watch) => (
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
  );
}
