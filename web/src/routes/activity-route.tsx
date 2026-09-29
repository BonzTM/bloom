import { useId, type ReactNode, type SyntheticEvent } from "react";
import { useSearchParams } from "react-router-dom";
import { z } from "zod/v4";
import {
  useSession,
  useSessionRecheck,
} from "../features/auth/hooks/auth-queries.js";
import {
  activityFilterSchema,
  playbackSourceSchema,
  playMethodSchema,
  type ActivityFilter,
} from "../features/playback/api/playback-schemas.js";
import {
  ActivityTable,
  sourceLabel,
} from "../features/playback/components/activity-table.js";
import { playMethodLabel } from "../features/playback/components/watch-format.js";
import { useActivity } from "../features/playback/hooks/playback-queries.js";
import { SignInNotConfirmed } from "../features/requests/components/list-states.js";
import { accessDenial } from "../lib/api/errors.js";
import { AccessDeniedRoute } from "./access-denied-route.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

// Every watch across servers and people, filtered. The filter lives in the
// URL so a view can be shared and the back button returns to it.
export default function ActivityRoute(): ReactNode {
  usePageTitle(pageTitle("Activity"));
  const session = useSession();
  const accountId = session.data?.account.id;
  if (accountId === undefined) {
    return null;
  }
  return <ActivityPage key={accountId} accountId={accountId} />;
}

function ActivityPage({
  accountId,
}: Readonly<{ accountId: string }>): ReactNode {
  const [params, setParams] = useSearchParams();
  const filter = readFilter(params);
  const activity = useActivity(accountId, filter);
  const denial = accessDenial(activity.error);
  useSessionRecheck(denial !== undefined, activity.errorUpdatedAt);
  if (denial === "forbidden") {
    return <AccessDeniedRoute />;
  }
  return (
    <>
      <h1>Activity</h1>
      <p className="page-intro">
        Every watch Bloom has recorded, across servers and people, newest first.
        Narrow it by server, title, delivery, source, or when it started; each
        person links to their statistics.
      </p>
      <section aria-labelledby="activity-heading" className="card">
        <h2 id="activity-heading">Watches</h2>
        <FilterForm
          filter={filter}
          servers={serversSeen(activity.data?.pages, filter.mediaServerId)}
          onChange={(next) => {
            setParams(writeFilter(next));
          }}
        />
        {denial === "unauthenticated" ? (
          <SignInNotConfirmed noun="activity" onRetry={activity.refetch} />
        ) : (
          <ActivityTable query={activity} />
        )}
      </section>
    </>
  );
}

type ServerOption = Readonly<{ id: string; name: string }>;

// The servers offered are the ones in the data loaded so far, plus the one
// already chosen so a filtered view never hides its own option.
function serversSeen(
  pages:
    | readonly {
        items: readonly {
          media_server_id: string;
          media_server_name: string;
        }[];
      }[]
    | undefined,
  chosen: string | undefined,
): readonly ServerOption[] {
  const seen = new Map<string, string>();
  for (const watch of (pages ?? []).flatMap((page) => page.items)) {
    seen.set(watch.media_server_id, watch.media_server_name);
  }
  if (chosen !== undefined && !seen.has(chosen)) {
    seen.set(chosen, chosen);
  }
  return [...seen.entries()]
    .map(([id, name]) => ({ id, name }))
    .sort((a, b) => a.name.localeCompare(b.name, "en"));
}

// ---- the filter in the URL

const QUERY_KEYS: readonly (readonly [keyof ActivityFilter, string])[] = [
  ["mediaServerId", "server"],
  ["q", "q"],
  ["playMethod", "method"],
  ["source", "source"],
  ["startedAfter", "after"],
  ["startedBefore", "before"],
];

// A URL written by hand may carry anything; each value is read on its own
// and a bad one is dropped rather than failing the page.
function readFilter(params: URLSearchParams): ActivityFilter {
  const filter: Record<string, string> = {};
  for (const [key, name] of QUERY_KEYS) {
    const raw = params.get(name);
    if (raw !== null && raw !== "") {
      filter[key] =
        key === "startedAfter" || key === "startedBefore"
          ? dayToInstant(raw, key === "startedBefore")
          : raw;
    }
  }
  const parsed = activityFilterSchema.safeParse(filter);
  return parsed.success ? parsed.data : readEachField(filter);
}

function readEachField(filter: Record<string, string>): ActivityFilter {
  const valid: Record<string, string> = {};
  for (const [key] of QUERY_KEYS) {
    const value = filter[key];
    if (value === undefined) {
      continue;
    }
    if (activityFilterSchema.safeParse({ [key]: value }).success) {
      valid[key] = value;
    }
  }
  return activityFilterSchema.parse(valid);
}

function writeFilter(filter: FormFilter): Record<string, string> {
  const next: Record<string, string> = {};
  for (const [key, name] of QUERY_KEYS) {
    const value = filter[key];
    if (value !== "") {
      next[name] = value;
    }
  }
  return next;
}

const daySchema = z.iso.date();

// A day from a date input becomes the instant it starts, or the instant the
// next day starts for an exclusive upper bound; browser local time.
function dayToInstant(day: string, endOfDay: boolean): string {
  if (!daySchema.safeParse(day).success) {
    return day;
  }
  const [year = 0, month = 1, date = 1] = day.split("-").map(Number);
  const local = new Date(year, month - 1, date + (endOfDay ? 1 : 0));
  return local.toISOString();
}

function instantToDay(instant: string | undefined, endOfDay: boolean): string {
  if (instant === undefined) {
    return "";
  }
  const local = new Date(instant);
  if (Number.isNaN(local.getTime())) {
    return "";
  }
  if (endOfDay) {
    local.setDate(local.getDate() - 1);
  }
  const pad = (value: number): string => String(value).padStart(2, "0");
  return `${String(local.getFullYear())}-${pad(local.getMonth() + 1)}-${pad(local.getDate())}`;
}

// ---- the filter form

type FormFilter = Readonly<Record<keyof ActivityFilter, string>>;

function text(value: FormDataEntryValue | null): string {
  return typeof value === "string" ? value.trim() : "";
}

function FilterForm({
  filter,
  servers,
  onChange,
}: Readonly<{
  filter: ActivityFilter;
  servers: readonly ServerOption[];
  onChange: (next: FormFilter) => void;
}>): ReactNode {
  const id = useId();
  function handleSubmit(event: SyntheticEvent<HTMLFormElement>): void {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    onChange({
      mediaServerId: text(data.get("server")),
      q: text(data.get("q")),
      playMethod: text(data.get("method")),
      source: text(data.get("source")),
      startedAfter: dayOrEmpty(text(data.get("after")), false),
      startedBefore: dayOrEmpty(text(data.get("before")), true),
    });
  }
  return (
    <form
      className="activity-filters"
      aria-label="Filter activity"
      onSubmit={handleSubmit}
      noValidate
      key={`${JSON.stringify(filter)}/${String(servers.length)}`}
    >
      <div>
        <label htmlFor={`${id}-q`}>Title contains</label>
        <input
          id={`${id}-q`}
          name="q"
          type="search"
          maxLength={128}
          defaultValue={filter.q ?? ""}
        />
      </div>
      <div>
        <label htmlFor={`${id}-server`}>Server</label>
        <select
          id={`${id}-server`}
          name="server"
          defaultValue={filter.mediaServerId ?? ""}
        >
          <option value="">All servers</option>
          {servers.map((server) => (
            <option key={server.id} value={server.id}>
              {server.name}
            </option>
          ))}
        </select>
      </div>
      <div>
        <label htmlFor={`${id}-method`}>Delivery</label>
        <select
          id={`${id}-method`}
          name="method"
          defaultValue={filter.playMethod ?? ""}
        >
          <option value="">Any</option>
          {playMethodSchema.options.map((option) => (
            <option key={option} value={option}>
              {playMethodLabel(option)}
            </option>
          ))}
        </select>
      </div>
      <div>
        <label htmlFor={`${id}-source`}>Source</label>
        <select
          id={`${id}-source`}
          name="source"
          defaultValue={filter.source ?? ""}
        >
          <option value="">Any</option>
          {playbackSourceSchema.options.map((option) => (
            <option key={option} value={option}>
              {sourceLabel(option)}
            </option>
          ))}
        </select>
      </div>
      <div>
        <label htmlFor={`${id}-after`}>From day</label>
        <input
          id={`${id}-after`}
          name="after"
          type="date"
          defaultValue={instantToDay(filter.startedAfter, false)}
        />
      </div>
      <div>
        <label htmlFor={`${id}-before`}>To day</label>
        <input
          id={`${id}-before`}
          name="before"
          type="date"
          defaultValue={instantToDay(filter.startedBefore, true)}
        />
      </div>
      <div className="form-actions">
        <button type="submit" className="btn-primary">
          Apply filters
        </button>
      </div>
    </form>
  );
}

function dayOrEmpty(day: string, endOfDay: boolean): string {
  return day === "" ? "" : dayToInstant(day, endOfDay);
}
