import type { PlayMethod, Watch } from "../api/playback-schemas.js";

const PLAY_METHOD_LABELS: Readonly<Record<PlayMethod, string>> = {
  direct_play: "Direct play",
  direct_stream: "Direct stream",
  transcode: "Transcode",
  unknown: "Unknown",
};

export function playMethodLabel(method: PlayMethod): string {
  return PLAY_METHOD_LABELS[method];
}

// "Series S02E05 · Episode name" for an episode, the item name otherwise.
// Server strings are shown as text; nothing here is interpreted.
export function itemTitle(watch: Watch): string {
  const name = watch.item_name === "" ? watch.item_id : watch.item_name;
  if (watch.series_name === "") {
    return name;
  }
  const numbering = episodeNumbering(watch);
  return numbering === ""
    ? `${watch.series_name} · ${name}`
    : `${watch.series_name} ${numbering} · ${name}`;
}

function episodeNumbering(watch: Watch): string {
  if (watch.season_number === null || watch.episode_number === null) {
    return "";
  }
  return `S${pad(watch.season_number)}E${pad(watch.episode_number)}`;
}

// A playback position as h:mm:ss, or m:ss under an hour.
export function formatPosition(positionMs: number): string {
  const totalSeconds = Math.floor(positionMs / 1000);
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  return hours > 0
    ? `${String(hours)}:${pad(minutes)}:${pad(seconds)}`
    : `${String(minutes)}:${pad(seconds)}`;
}

// Time actually spent watching, in the largest units that read naturally.
export function formatActiveTime(activeSeconds: number): string {
  const hours = Math.floor(activeSeconds / 3600);
  const minutes = Math.floor((activeSeconds % 3600) / 60);
  const seconds = activeSeconds % 60;
  if (hours > 0) {
    return `${String(hours)} h ${String(minutes)} min`;
  }
  if (minutes > 0) {
    return `${String(minutes)} min ${String(seconds)} s`;
  }
  return `${String(seconds)} s`;
}

// Who is watching on what: "alice on Living room TV (Jellyfin Web)".
export function whereLabel(watch: Watch): string {
  const device = watch.device_name === "" ? watch.device_id : watch.device_name;
  return watch.client === "" ? device : `${device} (${watch.client})`;
}

function pad(value: number): string {
  return String(value).padStart(2, "0");
}
