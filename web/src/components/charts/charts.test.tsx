import { describe, expect, it } from "@jest/globals";
import { render, screen, within } from "@testing-library/react";
import { BarChart } from "./bar-chart.js";
import { formatDuration, niceCeiling, normalizePoints } from "./chart-data.js";
import { LineChart } from "./line-chart.js";

describe("niceCeiling", () => {
  it("rounds up to a readable axis bound", () => {
    expect(niceCeiling(0)).toBe(1);
    expect(niceCeiling(7)).toBe(10);
    expect(niceCeiling(23)).toBe(25);
    expect(niceCeiling(1300)).toBe(2000);
    expect(niceCeiling(2)).toBe(2);
    expect(niceCeiling(50)).toBe(50);
  });
});

describe("formatDuration", () => {
  it("shows the largest two whole units", () => {
    expect(formatDuration(12)).toBe("12s");
    expect(formatDuration(2700)).toBe("45m");
    expect(formatDuration(2712)).toBe("45m 12s");
    expect(formatDuration(11_520)).toBe("3h 12m");
    expect(formatDuration(7200)).toBe("2h");
    expect(formatDuration(-5)).toBe("0s");
  });
});

describe("normalizePoints", () => {
  it("bounds the count and clamps bad values", () => {
    const many = Array.from({ length: 500 }, (_, i) => ({
      label: String(i),
      value: i,
    }));
    expect(normalizePoints(many)).toHaveLength(400);
    expect(
      normalizePoints([
        { label: "a", value: -3 },
        { label: "b", value: Number.NaN },
        { label: "c", value: 4 },
      ]).map((p) => p.value),
    ).toEqual([0, 0, 4]);
  });
});

describe("BarChart", () => {
  it("names the figure and offers the numbers as a table", () => {
    render(
      <BarChart
        title="Most watched movies"
        labelHeading="Title"
        valueHeading="Plays"
        points={[
          { label: "Heat", value: 12 },
          { label: "Ronin", value: 7 },
        ]}
      />,
    );
    const figure = screen.getByRole("figure", { name: "Most watched movies" });
    expect(figure).toBeVisible();
    const table = within(figure).getByRole("table", {
      name: "Most watched movies",
    });
    expect(
      within(table).getByRole("row", { name: /^Heat / }),
    ).toHaveTextContent("12");
    expect(within(figure).getByText("Show as table")).toBeVisible();
    expect(figure.querySelector("svg")).toHaveAttribute("aria-hidden", "true");
  });

  it("says when there is nothing to draw", () => {
    render(
      <BarChart
        title="Clients"
        labelHeading="Client"
        valueHeading="Plays"
        points={[]}
      />,
    );
    expect(screen.getByText("Nothing recorded in this window.")).toBeVisible();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });

  it("formats values with the given formatter in the table", () => {
    render(
      <BarChart
        title="Watch time by user"
        labelHeading="User"
        valueHeading="Watch time"
        orientation="vertical"
        points={[{ label: "alice", value: 11_520 }]}
        format={formatDuration}
      />,
    );
    expect(screen.getByRole("row", { name: /^alice / })).toHaveTextContent(
      "3h 12m",
    );
  });
});

describe("LineChart", () => {
  it("draws one series with the data as a table", () => {
    render(
      <LineChart
        title="Plays per day"
        labelHeading="Day"
        valueHeading="Plays"
        points={[
          { label: "2026-09-20", value: 3 },
          { label: "2026-09-21", value: 0 },
          { label: "2026-09-22", value: 5 },
        ]}
      />,
    );
    const figure = screen.getByRole("figure", { name: "Plays per day" });
    expect(figure.querySelector("path.chart-line")).not.toBeNull();
    expect(
      within(figure).getByRole("row", { name: /^2026-09-22 / }),
    ).toHaveTextContent("5");
  });

  it("copes with a single point", () => {
    render(
      <LineChart
        title="Plays per day"
        labelHeading="Day"
        valueHeading="Plays"
        points={[{ label: "2026-09-20", value: 3 }]}
      />,
    );
    expect(
      screen
        .getByRole("figure", { name: "Plays per day" })
        .querySelector("path.chart-line"),
    ).not.toBeNull();
  });
});
