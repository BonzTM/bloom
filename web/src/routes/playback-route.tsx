import { useId, useState, type ReactNode } from "react";
import { AsyncStatus } from "../components/async-status.js";
import {
  useSession,
  useSessionRecheck,
} from "../features/auth/hooks/auth-queries.js";
import { HistoryTable } from "../features/playback/components/history-table.js";
import { NowPlayingTable } from "../features/playback/components/now-playing-table.js";
import {
  useNowPlaying,
  usePlaybackHistory,
} from "../features/playback/hooks/playback-queries.js";
import { accessDenial } from "../lib/api/errors.js";
import { AccessDeniedRoute } from "./access-denied-route.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

export default function PlaybackRoute(): ReactNode {
  usePageTitle(pageTitle("Playback"));
  const session = useSession();
  const accountId = session.data?.account.id;
  // The route guard only renders this page for a signed-in account. Keying
  // the page on the account remounts it when the principal changes.
  if (accountId === undefined) {
    return null;
  }
  return <PlaybackPage key={accountId} accountId={accountId} />;
}

function PlaybackPage({
  accountId,
}: Readonly<{ accountId: string }>): ReactNode {
  const now = useNowPlaying(accountId);
  const [serverFilter, setServerFilter] = useState<string | undefined>(
    undefined,
  );
  // The unfiltered history stays mounted as the source of filter options, so
  // choosing a server never hides the other servers or the chosen one; the
  // filtered query feeds the table. With no filter the two are one query.
  const allHistory = usePlaybackHistory(accountId, {});
  const history = usePlaybackHistory(accountId, {
    ...(serverFilter === undefined ? {} : { mediaServerId: serverFilter }),
  });
  const denial = accessDenial(now.error) ?? accessDenial(history.error);
  // The server denied something the cached session says is allowed: the
  // session is gone, or a permission was taken away. Re-reading the session
  // lets the route guard send the person to sign in or off this page.
  useSessionRecheck(
    denial !== undefined,
    Math.max(now.errorUpdatedAt, history.errorUpdatedAt),
  );
  if (denial === "forbidden") {
    return <AccessDeniedRoute />;
  }
  if (denial === "unauthenticated") {
    return (
      <>
        <h1>Playback</h1>
        <SignInNotConfirmed
          onRetry={() => Promise.all([now.refetch(), history.refetch()])}
        />
      </>
    );
  }
  const servers = serversSeen(now.data?.items, allHistory.data?.pages);
  return (
    <>
      <h1>Playback</h1>
      <p className="page-intro">
        What is playing on your media servers right now, and what finished
        recently. Bloom polls each server and adds up the time a session was
        actually playing.
      </p>
      <section aria-labelledby="now-playing-heading" className="card">
        <h2 id="now-playing-heading">Playing now</h2>
        <NowPlayingTable query={now} />
      </section>
      <section aria-labelledby="history-heading" className="card">
        <h2 id="history-heading">Recent history</h2>
        <ServerFilter
          servers={servers}
          value={serverFilter}
          onChange={setServerFilter}
        />
        <HistoryTable query={history} />
      </section>
    </>
  );
}

type ServerOption = Readonly<{ id: string; name: string }>;

// The servers a person may filter by are the ones that appear in the data
// already loaded; a registered server with no watches has nothing to show.
function serversSeen(
  now:
    | readonly { media_server_id: string; media_server_name: string }[]
    | undefined,
  pages:
    | readonly {
        items: readonly {
          media_server_id: string;
          media_server_name: string;
        }[];
      }[]
    | undefined,
): readonly ServerOption[] {
  const seen = new Map<string, string>();
  for (const watch of [
    ...(now ?? []),
    ...(pages ?? []).flatMap((p) => p.items),
  ]) {
    seen.set(watch.media_server_id, watch.media_server_name);
  }
  return [...seen.entries()]
    .map(([id, name]) => ({ id, name }))
    .sort((a, b) => a.name.localeCompare(b.name, "en"));
}

function ServerFilter({
  servers,
  value,
  onChange,
}: Readonly<{
  servers: readonly ServerOption[];
  value: string | undefined;
  onChange: (id: string | undefined) => void;
}>): ReactNode {
  const id = useId();
  if (servers.length < 2 && value === undefined) {
    return null;
  }
  return (
    <div className="filter">
      <label htmlFor={id}>Server</label>
      <select
        id={id}
        value={value ?? ""}
        onChange={(event) => {
          onChange(event.target.value === "" ? undefined : event.target.value);
        }}
      >
        <option value="">All servers</option>
        {servers.map((server) => (
          <option key={server.id} value={server.id}>
            {server.name}
          </option>
        ))}
      </select>
    </div>
  );
}

function SignInNotConfirmed({
  onRetry,
}: Readonly<{ onRetry: () => Promise<unknown> }>): ReactNode {
  return (
    <>
      <AsyncStatus kind="alert">
        Playback could not be loaded because your sign-in could not be
        confirmed.
      </AsyncStatus>
      <button
        type="button"
        onClick={() => {
          void onRetry();
        }}
      >
        Retry
      </button>
    </>
  );
}
