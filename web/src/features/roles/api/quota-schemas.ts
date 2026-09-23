import { z } from "zod/v4";

// Wire contract for `/api/v1/roles/{id}/request-quota`, mirroring
// `RequestQuotaInput` and `RequestQuota` in api/openapi.yaml. A limit of zero
// with a period of zero means that kind of media is not limited.
const MAX_PERIOD_DAYS = 3650;

const count = z.number().int().min(0);
const periodDays = z.number().int().min(0).max(MAX_PERIOD_DAYS);

// The exact body the server accepts: every field present, nothing else.
export const requestQuotaInputSchema = z.strictObject({
  movie_limit: count,
  movie_period_days: periodDays,
  season_limit: count,
  season_period_days: periodDays,
});

export type RequestQuotaInput = z.output<typeof requestQuotaInputSchema>;

export const requestQuotaSchema = requestQuotaInputSchema.extend({
  scope_id: z.uuid(),
});

export type RequestQuota = z.output<typeof requestQuotaSchema>;

export const quotaLimits = { maxPeriodDays: MAX_PERIOD_DAYS } as const;
