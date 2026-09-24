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

type BarChartProps = Readonly<{
  title: string;
  labelHeading: string;
  valueHeading: string;
  points: readonly ChartPoint[];
  format?: ChartFormat;
  // Horizontal bars suit ranked lists with long labels; vertical bars suit
  // short categorical axes such as weekdays or hours.
  orientation?: "horizontal" | "vertical";
}>;

const WIDTH = 600;
const ROW = 28;
const LABEL_WIDTH = 160;
const V_HEIGHT = 240;
const V_AXIS = 28;

// A bar chart drawn in SVG from design tokens, with the data as a table.
// The figure carries the title; the drawing itself is hidden from the
// accessibility tree because the table says the same thing better.
export function BarChart({
  title,
  labelHeading,
  valueHeading,
  points,
  format = formatCount,
  orientation = "horizontal",
}: BarChartProps): ReactNode {
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
  return (
    <figure className="chart" aria-labelledby={id}>
      <figcaption id={id}>{title}</figcaption>
      {orientation === "horizontal" ? (
        <HorizontalBars data={data} ceiling={ceiling} format={format} />
      ) : (
        <VerticalBars data={data} ceiling={ceiling} format={format} />
      )}
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

type BarsProps = Readonly<{
  data: readonly ChartPoint[];
  ceiling: number;
  format: ChartFormat;
}>;

function HorizontalBars({ data, ceiling, format }: BarsProps): ReactNode {
  const height = data.length * ROW;
  const plotWidth = WIDTH - LABEL_WIDTH - 72;
  return (
    <svg
      className="chart-svg"
      viewBox={`0 0 ${String(WIDTH)} ${String(height)}`}
      aria-hidden="true"
      focusable="false"
    >
      {data.map((point, index) => {
        const y = index * ROW;
        const width = (point.value / ceiling) * plotWidth;
        return (
          <g key={`${String(index)}-${point.label}`}>
            <text
              x={LABEL_WIDTH - 8}
              y={y + ROW / 2}
              className="chart-label"
              textAnchor="end"
              dominantBaseline="middle"
            >
              {clip(point.label, 24)}
            </text>
            <rect
              x={LABEL_WIDTH}
              y={y + 6}
              width={Math.max(width, 1)}
              height={ROW - 12}
              rx={3}
              className="chart-bar"
            />
            <text
              x={LABEL_WIDTH + width + 6}
              y={y + ROW / 2}
              className="chart-value"
              dominantBaseline="middle"
            >
              {format(point.value)}
            </text>
          </g>
        );
      })}
    </svg>
  );
}

function VerticalBars({ data, ceiling, format }: BarsProps): ReactNode {
  const plotHeight = V_HEIGHT - V_AXIS;
  const slot = WIDTH / data.length;
  const barWidth = Math.max(slot * 0.6, 2);
  return (
    <svg
      className="chart-svg"
      viewBox={`0 0 ${String(WIDTH)} ${String(V_HEIGHT)}`}
      aria-hidden="true"
      focusable="false"
    >
      <line
        x1={0}
        x2={WIDTH}
        y1={plotHeight}
        y2={plotHeight}
        className="chart-axis"
      />
      {data.map((point, index) => {
        const height = (point.value / ceiling) * (plotHeight - 16);
        const x = index * slot + (slot - barWidth) / 2;
        return (
          <g key={`${String(index)}-${point.label}`}>
            <rect
              x={x}
              y={plotHeight - height}
              width={barWidth}
              height={height}
              rx={2}
              className="chart-bar"
            >
              <title>{`${point.label}: ${format(point.value)}`}</title>
            </rect>
            {data.length <= 31 ? (
              <text
                x={x + barWidth / 2}
                y={V_HEIGHT - 8}
                className="chart-label"
                textAnchor="middle"
              >
                {clip(point.label, 6)}
              </text>
            ) : null}
          </g>
        );
      })}
    </svg>
  );
}

function clip(label: string, max: number): string {
  return label.length > max ? `${label.slice(0, max - 1)}…` : label;
}
