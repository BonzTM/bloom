import { expect, it } from "@jest/globals";
import {
  deliverySchema,
  notificationEventTypeSchema,
  notificationPreferencesSchema,
  subscriptionsSchema,
} from "./notification-schemas.js";

const delivery = {
  id: "0b6c3d2e-1111-4a2b-9c3d-000000000010",
  event_type: "available",
  status: "sent",
  attempts: 1,
  last_error: "",
  next_attempt_at: "2026-09-24T19:00:00Z",
  created_at: "2026-09-24T19:00:00Z",
  updated_at: "2026-09-24T19:00:00Z",
};

it("lets a channel subscribe to every event kind the contract lists", () => {
  const all = [...notificationEventTypeSchema.options];
  expect(all).toHaveLength(7);
  expect(subscriptionsSchema.safeParse(all).success).toBe(true);
  expect(subscriptionsSchema.safeParse([...all, "created"]).success).toBe(
    false,
  );
});

it("accepts a playback delivery that carries a watch instead of a request", () => {
  const request = {
    ...delivery,
    request_id: "5e4d3c2b-0000-4000-8000-000000000005",
  };
  const watch = {
    ...delivery,
    event_type: "playback.session_started",
    watch_id: "7b2c3d4e-0000-4000-8000-000000000001",
  };
  expect(deliverySchema.safeParse(request).success).toBe(true);
  expect(deliverySchema.safeParse(watch).success).toBe(true);
  expect(deliverySchema.safeParse(delivery).success).toBe(true);
});

it("requires the complete preference matrix exactly once", () => {
  const complete = notificationEventTypeSchema.options.map((event_type) => ({
    event_type,
    enabled: true,
  }));
  expect(notificationPreferencesSchema.safeParse(complete).success).toBe(true);
  expect(
    notificationPreferencesSchema.safeParse(complete.slice(1)).success,
  ).toBe(false);
  expect(
    notificationPreferencesSchema.safeParse([
      ...complete.slice(1),
      complete[0],
      complete[0],
    ]).success,
  ).toBe(false);
});
