import type {
  PlayMethod,
  StreamDetails,
  Watch,
} from "../api/playback-schemas.js";

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

// "1080p · h264/aac · 8.2 Mbit/s", or the parts that are known; an empty
// string when nothing is known. Transcode reasons are listed separately.
export function streamSummary(stream: StreamDetails | undefined): string {
  if (stream === undefined) {
    return "";
  }
  const parts: string[] = [];
  if (stream.height !== undefined && stream.height > 0) {
    parts.push(`${String(stream.height)}p`);
  }
  const codecs = [stream.video_codec, stream.audio_codec].filter(
    (codec): codec is string => codec !== undefined && codec !== "",
  );
  if (codecs.length > 0) {
    parts.push(codecs.join("/"));
  }
  if (stream.bitrate !== undefined && stream.bitrate > 0) {
    parts.push(`${(stream.bitrate / 1_000_000).toFixed(1)} Mbit/s`);
  }
  return parts.join(" · ");
}

export function transcodeReasonsLabel(
  stream: StreamDetails | undefined,
): string {
  const reasons = stream?.transcode_reasons ?? [];
  return reasons.join(", ");
}

export function watchPath(watchId: string): string {
  return `/admin/playback/watches/${encodeURIComponent(watchId)}`;
}
