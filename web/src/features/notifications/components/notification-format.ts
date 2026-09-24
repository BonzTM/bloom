import type {
  NotificationEventType,
  NotificationKind,
} from "../api/notification-schemas.js";

const KIND_LABELS: Readonly<Record<NotificationKind, string>> = {
  webhook: "Webhook",
  discord: "Discord",
  email: "Email",
};

const EVENT_LABELS: Readonly<Record<NotificationEventType, string>> = {
  created: "Request created",
  approved: "Request approved",
  declined: "Request declined",
  dispatched: "Sent to the download manager",
  available: "Available to watch",
  failed: "Fulfilment failed",
};

export function kindLabel(kind: NotificationKind): string {
  return KIND_LABELS[kind];
}

export function eventLabel(event: NotificationEventType): string {
  return EVENT_LABELS[event];
}

const STATUS_BADGES = {
  pending: "badge badge-warning",
  sent: "badge badge-success",
  failed: "badge badge-danger",
} as const;

export function deliveryBadgeClass(status: keyof typeof STATUS_BADGES): string {
  return STATUS_BADGES[status];
}
