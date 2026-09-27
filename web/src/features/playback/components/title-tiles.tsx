import { useId, type ReactNode } from "react";
import {
  formatCount,
  formatDuration,
} from "../../../components/charts/chart-data.js";
import type { StatsTitle } from "../api/stats-schemas.js";
import { ItemImage } from "./item-image.js";

export type TitleMetric = "plays" | "unique_users" | "watch_seconds";

type TitleTilesProps = Readonly<{
  title: string;
  titles: readonly StatsTitle[];
  metric: TitleMetric;
}>;

// A ranked row of artwork tiles, the way Jellystat shows most-watched and
// most-popular titles. The figure carries the heading; each tile names the
// title and its figure for the chosen metric.
export function TitleTiles({
  title,
  titles,
  metric,
}: TitleTilesProps): ReactNode {
  const id = useId();
  return (
    <figure className="tile-figure" aria-labelledby={id}>
      <figcaption id={id}>{title}</figcaption>
      {titles.length === 0 ? (
        <p className="chart-empty">Nothing recorded in this window.</p>
      ) : (
        <ol className="tile-grid">
          {titles.map((item, index) => (
            <li key={`${item.media_server_id}/${item.key}`} className="tile">
              <span className="tile-rank" aria-hidden="true">
                {index + 1}
              </span>
              <ItemImage
                mediaServerId={item.media_server_id}
                itemId={item.kind === "movie" ? item.key : undefined}
                title={item.name}
                shape="poster"
              />
              <span className="tile-name">{item.name}</span>
              <span className="tile-metric">{metricLabel(item, metric)}</span>
            </li>
          ))}
        </ol>
      )}
    </figure>
  );
}

export function metricLabel(item: StatsTitle, metric: TitleMetric): string {
  switch (metric) {
    case "plays":
      return `${formatCount(item.plays)} ${item.plays === 1 ? "play" : "plays"}`;
    case "unique_users":
      return `${formatCount(item.unique_users)} ${item.unique_users === 1 ? "person" : "people"}`;
    case "watch_seconds":
      return formatDuration(item.watch_seconds);
  }
}
