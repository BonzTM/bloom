import type { ImportSource, ImportState } from "../api/imports-schemas.js";

const SOURCE_LABELS: Readonly<Record<ImportSource, string>> = {
  playback_reporting: "Playback Reporting",
  jellystat: "Jellystat backup",
  bloom_export: "Bloom export",
};

const STATE_LABELS: Readonly<Record<ImportState, string>> = {
  pending: "Waiting",
  running: "Running",
  completed: "Completed",
  failed: "Failed",
  cancelled: "Cancelled",
};

const STATE_BADGES: Readonly<Record<ImportState, string>> = {
  pending: "badge-neutral",
  running: "badge-warning",
  completed: "badge-success",
  failed: "badge-danger",
  cancelled: "badge-neutral",
};

export function sourceLabel(source: ImportSource): string {
  return SOURCE_LABELS[source];
}

export function stateLabel(state: ImportState): string {
  return STATE_LABELS[state];
}

export function stateBadgeClass(state: ImportState): string {
  return `badge ${STATE_BADGES[state]}`;
}

// "2026-09-27 14:05" from an RFC 3339 timestamp; the API sends UTC.
export function formatStamp(value: string | undefined): string {
  return value === undefined ? "" : value.slice(0, 16).replace("T", " ");
}
