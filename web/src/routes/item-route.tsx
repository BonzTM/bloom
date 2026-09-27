import type { ReactNode } from "react";
import { Link, useParams } from "react-router-dom";
import { AsyncStatus } from "../components/async-status.js";
import {
  formatCount,
  formatDuration,
} from "../components/charts/chart-data.js";
import {
  useSession,
  useSessionRecheck,
} from "../features/auth/hooks/auth-queries.js";
import type { CatalogItemDetail } from "../features/catalog/api/catalog-schemas.js";
import {
  formatDay,
  formatRuntime,
  itemPath,
  itemTitle,
  libraryPath,
  typePlural,
} from "../features/catalog/components/catalog-format.js";
import { ItemHistoryTable } from "../features/catalog/components/item-history-table.js";
import {
  useCatalogItem,
  useItemHistory,
} from "../features/catalog/hooks/catalog-queries.js";
import { ItemImage } from "../features/playback/components/item-image.js";
import { ReportFailed } from "../features/playback/components/stats-panels.js";
import { accessDenial } from "../lib/api/errors.js";
import { AccessDeniedRoute } from "./access-denied-route.js";
import { NotFoundRoute } from "./not-found-route.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

// One catalog item: artwork, facts, how much it was watched, and by whom.
export default function ItemRoute(): ReactNode {
  const { serverId = "", itemId = "" } = useParams();
  const session = useSession();
  const accountId = session.data?.account.id;
  if (serverId === "" || itemId === "") {
    return <NotFoundRoute />;
  }
  if (accountId === undefined) {
    return null;
  }
  return (
    <ItemPage
      key={`${accountId}/${serverId}/${itemId}`}
      accountId={accountId}
      serverId={serverId}
      itemId={itemId}
    />
  );
}

type ItemPageProps = Readonly<{
  accountId: string;
  serverId: string;
  itemId: string;
}>;

function ItemPage({ accountId, serverId, itemId }: ItemPageProps): ReactNode {
  const detail = useCatalogItem(accountId, serverId, itemId);
  const history = useItemHistory(accountId, serverId, itemId);
  usePageTitle(pageTitle(detail.data?.item.name ?? "Item"));
  const denial = accessDenial(detail.error) ?? accessDenial(history.error);
  useSessionRecheck(
    denial !== undefined,
    Math.max(detail.errorUpdatedAt, history.errorUpdatedAt),
  );
  if (denial === "forbidden") {
    return <AccessDeniedRoute />;
  }
  if (detail.status === "pending") {
    return <AsyncStatus>Loading the item…</AsyncStatus>;
  }
  if (detail.status === "error") {
    return (
      <>
        <h1>Item</h1>
        <ReportFailed what="The item" onRetry={detail.refetch} />
      </>
    );
  }
  return (
    <article className="title-page">
      <ItemHero detail={detail.data} />
      <section aria-labelledby="item-plays-heading" className="card">
        <h2 id="item-plays-heading">Plays</h2>
        <PlaySummary detail={detail.data} />
      </section>
      <section aria-labelledby="item-history-heading" className="card">
        <h2 id="item-history-heading">Who watched it</h2>
        <ItemHistoryTable query={history} />
      </section>
    </article>
  );
}

function ItemHero({
  detail,
}: Readonly<{ detail: CatalogItemDetail }>): ReactNode {
  const item = detail.item;
  const runtime = formatRuntime(item.runtime_ms);
  return (
    <header className="title-hero">
      <div className="title-hero-inner">
        <div className="title-poster">
          <ItemImage
            mediaServerId={item.media_server_id}
            itemId={item.item_id}
            title={item.name}
            shape={item.item_type === "Episode" ? "wide" : "poster"}
          />
        </div>
        <div className="title-heading">
          <p>
            <Link to={libraryPath(item.media_server_id, item.library_id)}>
              Back to the library
            </Link>
          </p>
          <h1>{itemTitle(item)}</h1>
          <p>
            <span className="badge badge-neutral">{item.item_type}</span>
            {item.archived ? (
              <span className="badge badge-warning">
                {" "}
                No longer on the server
              </span>
            ) : null}
          </p>
          <dl className="library-card-stats">
            {runtime === "" ? null : (
              <div>
                <dt>Runtime</dt>
                <dd>{runtime}</dd>
              </div>
            )}
            {item.premiere_date === undefined ? null : (
              <div>
                <dt>Released</dt>
                <dd>{formatDay(item.premiere_date)}</dd>
              </div>
            )}
            {item.community_rating === undefined ? null : (
              <div>
                <dt>Rating</dt>
                <dd>{item.community_rating.toFixed(1)}</dd>
              </div>
            )}
            {item.date_created === undefined ? null : (
              <div>
                <dt>Added</dt>
                <dd>{formatDay(item.date_created)}</dd>
              </div>
            )}
          </dl>
          {item.genres.length === 0 ? null : (
            <p className="row-detail">{item.genres.join(", ")}</p>
          )}
          {item.series_id === "" ? null : (
            <p>
              <Link to={itemPath(item.media_server_id, item.series_id)}>
                {item.series_name === "" ? "Series" : item.series_name}
              </Link>
            </p>
          )}
          {detail.children.length === 0 ? null : (
            <p className="row-detail">
              {detail.children
                .map(
                  (c) => `${formatCount(c.items)} ${typePlural(c.item_type)}`,
                )
                .join(" · ")}
            </p>
          )}
        </div>
      </div>
    </header>
  );
}

function PlaySummary({
  detail,
}: Readonly<{ detail: CatalogItemDetail }>): ReactNode {
  const item = detail.item;
  const cards = [
    { label: "Plays", value: formatCount(item.plays) },
    { label: "Watch time", value: formatDuration(item.watch_seconds) },
    { label: "People", value: formatCount(item.unique_users) },
    {
      label: "First played",
      value: formatDay(item.first_played_at) || "Never",
    },
    { label: "Last played", value: formatDay(item.last_played_at) || "Never" },
  ];
  return (
    <ul className="stat-cards" aria-label="Play summary">
      {cards.map((card) => (
        <li key={card.label} className="stat-card">
          <span className="stat-card-label">{card.label}</span>
          <strong className="stat-card-value">{card.value}</strong>
        </li>
      ))}
    </ul>
  );
}
