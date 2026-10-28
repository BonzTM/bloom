import type {
  MediaKind,
  MediaRequest,
  RequestStatus,
} from "../api/requests-schemas.js";

const STATUS_LABELS: Readonly<Record<RequestStatus, string>> = {
  pending: "Pending",
  approved: "Approved",
  declined: "Declined",
  processing: "Processing",
  available: "Available",
  failed: "Failed",
};

const STATUS_BADGES: Readonly<Record<RequestStatus, string>> = {
  pending: "badge-warning",
  approved: "badge-info",
  declined: "badge-neutral",
  processing: "badge-info",
  available: "badge-success",
  failed: "badge-danger",
};

const KIND_LABELS: Readonly<Record<MediaKind, string>> = {
  movie: "Movie",
  series: "Series",
};

export function statusLabel(status: RequestStatus): string {
  return STATUS_LABELS[status];
}

export function statusBadgeClass(status: RequestStatus): string {
  return `badge ${STATUS_BADGES[status]}`;
}

export function kindLabel(kind: MediaKind): string {
  return KIND_LABELS[kind];
}

// "Heat (1995)"; a year of zero means the provider did not know it.
export function titleWithYear(
  request: Pick<MediaRequest, "title" | "year">,
): string {
  return request.year === 0
    ? request.title
    : `${request.title} (${String(request.year)})`;
}

// "Seasons 1, 2 and 4", or an empty string for a movie.
export function seasonsLabel(
  request: Pick<MediaRequest, "kind" | "seasons">,
): string {
  if (request.kind !== "series" || request.seasons.length === 0) {
    return "";
  }
  const numbers = request.seasons.map((season) => String(season.number));
  if (numbers.length === 1) {
    return `Season ${numbers[0] ?? ""}`;
  }
  const last = numbers.pop() ?? "";
  return `Seasons ${numbers.join(", ")} and ${last}`;
}
