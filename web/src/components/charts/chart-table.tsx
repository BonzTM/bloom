import type { ReactNode } from "react";
import type { ChartFormat, ChartPoint } from "./chart-data.js";

type ChartTableProps = Readonly<{
  caption: string;
  labelHeading: string;
  valueHeading: string;
  points: readonly ChartPoint[];
  format: ChartFormat;
}>;

// The same numbers as a table. The drawing is decorative; this is what a
// screen reader reads and what a person can open when the bars are too
// small to tell apart.
export function ChartTable({
  caption,
  labelHeading,
  valueHeading,
  points,
  format,
}: ChartTableProps): ReactNode {
  return (
    <details className="chart-table">
      <summary>Show as table</summary>
      <div
        className="table-scroll"
        role="region"
        aria-label={caption}
        tabIndex={0}
      >
        <table>
          <caption>{caption}</caption>
          <thead>
            <tr>
              <th scope="col">{labelHeading}</th>
              <th scope="col">{valueHeading}</th>
            </tr>
          </thead>
          <tbody>
            {points.map((point, index) => (
              <tr key={`${String(index)}-${point.label}`}>
                <th scope="row">{point.label}</th>
                <td>{format(point.value)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </details>
  );
}
