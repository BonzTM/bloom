// Shared shapes and helpers for the chart primitives.

export type ChartPoint = Readonly<{
  // What the point is: a title, a client, a date.
  label: string;
  value: number;
}>;

export type ChartFormat = (value: number) => string;

const MAX_POINTS = 400;

export const chartLimits = { maxPoints: MAX_POINTS } as const;

// Charts draw at most a bounded number of points; a caller with more
// should aggregate first. Values must be finite and non-negative.
export function normalizePoints(points: readonly ChartPoint[]): ChartPoint[] {
  return points.slice(0, MAX_POINTS).map((point) => ({
    label: point.label,
    value: Number.isFinite(point.value) && point.value > 0 ? point.value : 0,
  }));
}

export function maxValue(points: readonly ChartPoint[]): number {
  return points.reduce((max, point) => Math.max(max, point.value), 0);
}

// A "nice" upper bound for an axis: 0 → 1, 7 → 10, 23 → 25, 1300 → 2000.
export function niceCeiling(value: number): number {
  if (value <= 0) {
    return 1;
  }
  const magnitude = 10 ** Math.floor(Math.log10(value));
  const scaled = value / magnitude;
  const step =
    scaled <= 1
      ? 1
      : scaled <= 2
        ? 2
        : scaled <= 2.5
          ? 2.5
          : scaled <= 5
            ? 5
            : 10;
  return step * magnitude;
}

export function formatCount(value: number): string {
  return new Intl.NumberFormat("en", { maximumFractionDigits: 0 }).format(
    value,
  );
}

// "3h 12m", "45m", "12s"; whole units only, largest two.
export function formatDuration(seconds: number): string {
  const total = Math.max(0, Math.round(seconds));
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const rest = total % 60;
  if (hours > 0) {
    return minutes > 0
      ? `${String(hours)}h ${String(minutes)}m`
      : `${String(hours)}h`;
  }
  if (minutes > 0) {
    return rest > 0
      ? `${String(minutes)}m ${String(rest)}s`
      : `${String(minutes)}m`;
  }
  return `${String(rest)}s`;
}
