import { useId, type ReactNode } from "react";
import {
  formatCount,
  maxValue,
  niceCeiling,
  normalizePoints,
  type ChartFormat,
  type ChartPoint,
} from "./chart-data.js";
import { ChartTable } from "./chart-table.js";

type LineChartProps = Readonly<{
  title: string;
  labelHeading: string;
  valueHeading: string;
  points: readonly ChartPoint[];
  format?: ChartFormat;
}>;

const WIDTH = 600;
const HEIGHT = 220;
const PAD_LEFT = 48;
const PAD_BOTTOM = 28;
const PAD_TOP = 12;

// A single series over an ordered axis such as days, drawn as a line with
// a filled area, gridlines at nice steps, and the data as a table.
export function LineChart({
  title,
  labelHeading,
  valueHeading,
  points,
  format = formatCount,
}: LineChartProps): ReactNode {
  const id = useId();
  const data = normalizePoints(points);
  if (data.length === 0) {
    return (
      <figure className="chart" aria-labelledby={id}>
        <figcaption id={id}>{title}</figcaption>
        <p className="chart-empty">Nothing recorded in this window.</p>
      </figure>
    );
  }
  const ceiling = niceCeiling(maxValue(data));
  const plotWidth = WIDTH - PAD_LEFT - 8;
  const plotHeight = HEIGHT - PAD_BOTTOM - PAD_TOP;
  const step = data.length === 1 ? 0 : plotWidth / (data.length - 1);
  const coords = data.map((point, index) => ({
    x: PAD_LEFT + index * step,
    y: PAD_TOP + plotHeight - (point.value / ceiling) * plotHeight,
  }));
  const line = coords
    .map(
      (c, index) =>
        `${index === 0 ? "M" : "L"}${c.x.toFixed(1)} ${c.y.toFixed(1)}`,
    )
    .join(" ");
  const area = `${line} L${(PAD_LEFT + (data.length - 1) * step).toFixed(1)} ${String(PAD_TOP + plotHeight)} L${String(PAD_LEFT)} ${String(PAD_TOP + plotHeight)} Z`;
  return (
    <figure className="chart" aria-labelledby={id}>
      <figcaption id={id}>{title}</figcaption>
      <svg
        className="chart-svg"
        viewBox={`0 0 ${String(WIDTH)} ${String(HEIGHT)}`}
        aria-hidden="true"
        focusable="false"
      >
        {[0, 0.5, 1].map((fraction) => {
          const y = PAD_TOP + plotHeight - fraction * plotHeight;
          return (
            <g key={fraction}>
              <line
                x1={PAD_LEFT}
                x2={WIDTH - 8}
                y1={y}
                y2={y}
                className="chart-grid"
              />
              <text
                x={PAD_LEFT - 6}
                y={y}
                className="chart-label"
                textAnchor="end"
                dominantBaseline="middle"
              >
                {format(ceiling * fraction)}
              </text>
            </g>
          );
        })}
        <path d={area} className="chart-area" />
        <path d={line} className="chart-line" fill="none" />
        {data.length <= 62
          ? coords.map((c, index) => (
              <circle
                key={`${String(index)}-${data[index]?.label ?? ""}`}
                cx={c.x}
                cy={c.y}
                r={2.5}
                className="chart-point"
              />
            ))
          : null}
        <AxisLabels data={data} step={step} />
      </svg>
      <ChartTable
        caption={title}
        labelHeading={labelHeading}
        valueHeading={valueHeading}
        points={data}
        format={format}
      />
    </figure>
  );
}

// First, last, and a few evenly spaced labels; never a crowded axis.
function AxisLabels({
  data,
  step,
}: Readonly<{ data: readonly ChartPoint[]; step: number }>): ReactNode {
  const every = Math.max(1, Math.ceil(data.length / 6));
  const lastIndex = data.length - 1;
  return (
    <>
      {data.map((point, index) => {
        const last = index === lastIndex;
        // The last label always shows; a periodic one too close to it is
        // dropped so the two never overlap.
        const periodic = index % every === 0 && lastIndex - index >= every / 2;
        if (!periodic && !last) {
          return null;
        }
        return (
          <text
            key={`${String(index)}-${point.label}`}
            x={PAD_LEFT + index * step}
            y={HEIGHT - 8}
            className="chart-label"
            textAnchor={index === 0 ? "start" : last ? "end" : "middle"}
          >
            {shortDate(point.label)}
          </text>
        );
      })}
    </>
  );
}

// An ISO date shows as month and day; anything else as itself.
function shortDate(label: string): string {
  return /^\d{4}-\d{2}-\d{2}$/.test(label) ? label.slice(5) : label;
}
