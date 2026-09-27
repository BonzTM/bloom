import { useId, useState, type ReactNode } from "react";
import { Link, useParams } from "react-router-dom";
import { AsyncStatus } from "../components/async-status.js";
import { BarChart } from "../components/charts/bar-chart.js";
import {
  useSession,
  useSessionRecheck,
} from "../features/auth/hooks/auth-queries.js";
import {
  catalogOrderSchema,
  catalogSortSchema,
  type CatalogOrder,
  type CatalogSort,
} from "../features/catalog/api/catalog-schemas.js";
import { ItemTiles } from "../features/catalog/components/item-tiles.js";
import {
  useCatalogItems,
  useLibraries,
  useLibraryGenres,
  useRecentItems,
  useStaleItems,
} from "../features/catalog/hooks/catalog-queries.js";
import { ReportFailed } from "../features/playback/components/stats-panels.js";
import { Pager } from "../features/requests/components/list-states.js";
import { accessDenial } from "../lib/api/errors.js";
import { AccessDeniedRoute } from "./access-denied-route.js";
import { NotFoundRoute } from "./not-found-route.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

const WINDOW_DAYS = 30;
const STALE_DAYS = 90;

const SORT_LABELS: Readonly<Record<CatalogSort, string>> = {
  name: "Name",
  date_added: "Date added",
  premiere_date: "Release date",
  plays: "Plays",
  watch_time: "Watch time",
  last_played: "Last played",
};

// One library: recently added, everything in it (sortable), genres, and
// what nobody has played in a while.
export default function LibraryRoute(): ReactNode {
  const { serverId = "", libraryId = "" } = useParams();
  const session = useSession();
  const accountId = session.data?.account.id;
  if (serverId === "" || libraryId === "") {
    return <NotFoundRoute />;
  }
  if (accountId === undefined) {
    return null;
  }
  return (
    <LibraryPage
      key={`${accountId}/${serverId}/${libraryId}`}
      accountId={accountId}
      serverId={serverId}
      libraryId={libraryId}
    />
  );
}

type LibraryPageProps = Readonly<{
  accountId: string;
  serverId: string;
  libraryId: string;
}>;

function LibraryPage({
  accountId,
  serverId,
  libraryId,
}: LibraryPageProps): ReactNode {
  const [sort, setSort] = useState<CatalogSort>("date_added");
  const [order, setOrder] = useState<CatalogOrder>("desc");
  const libraries = useLibraries(accountId, serverId);
  const items = useCatalogItems(accountId, serverId, libraryId, {
    days: WINDOW_DAYS,
    sort,
    order,
  });
  const recent = useRecentItems(accountId, serverId, libraryId);
  const genres = useLibraryGenres(accountId, serverId, libraryId, WINDOW_DAYS);
  const stale = useStaleItems(accountId, serverId, libraryId, STALE_DAYS);
  const name =
    libraries.data?.find((library) => library.id === libraryId)?.name ??
    libraryId;
  usePageTitle(pageTitle(name));
  const denial = accessDenial(items.error) ?? accessDenial(libraries.error);
  useSessionRecheck(
    denial !== undefined,
    Math.max(items.errorUpdatedAt, libraries.errorUpdatedAt),
  );
  if (denial === "forbidden") {
    return <AccessDeniedRoute />;
  }
  return (
    <>
      <p>
        <Link to="/admin/libraries">Back to libraries</Link>
      </p>
      <h1>{name}</h1>
      <section aria-labelledby="recent-heading" className="card">
        <h2 id="recent-heading">Recently added</h2>
        {recent.status === "pending" ? (
          <AsyncStatus>Loading recently added…</AsyncStatus>
        ) : recent.status === "error" ? (
          <ReportFailed what="Recently added" onRetry={recent.refetch} />
        ) : (
          <ItemTiles
            title="Newest items"
            items={recent.data}
            empty="Nothing has been added since the catalog was walked."
          />
        )}
      </section>
      <section aria-labelledby="items-heading" className="card">
        <h2 id="items-heading">Everything in {name}</h2>
        <SortControls
          sort={sort}
          order={order}
          onSort={setSort}
          onOrder={setOrder}
        />
        {items.status === "pending" ? (
          <AsyncStatus>Loading items…</AsyncStatus>
        ) : items.status === "error" && items.data === undefined ? (
          <ReportFailed what="The items" onRetry={items.refetch} />
        ) : (
          <>
            <ItemTiles
              title={`Items by ${SORT_LABELS[sort].toLowerCase()}`}
              items={items.data.pages.flatMap((page) => page.items)}
              empty="This library has no items in the catalog yet."
            />
            <Pager
              noun="items"
              hasMore={items.hasNextPage}
              loading={items.isFetchingNextPage}
              failed={items.isFetchNextPageError}
              onMore={items.fetchNextPage}
            />
          </>
        )}
      </section>
      <section aria-labelledby="genres-heading" className="card">
        <h2 id="genres-heading">Genres</h2>
        {genres.status === "pending" ? (
          <AsyncStatus>Loading genres…</AsyncStatus>
        ) : genres.status === "error" ? (
          <ReportFailed what="Genres" onRetry={genres.refetch} />
        ) : (
          <div className="chart-grid">
            <BarChart
              title="Items by genre"
              labelHeading="Genre"
              valueHeading="Items"
              points={genres.data.map((g) => ({
                label: g.genre,
                value: g.items,
              }))}
            />
            <BarChart
              title="Plays by genre"
              labelHeading="Genre"
              valueHeading="Plays"
              points={genres.data.map((g) => ({
                label: g.genre,
                value: g.plays,
              }))}
            />
          </div>
        )}
      </section>
      <section aria-labelledby="stale-heading" className="card">
        <h2 id="stale-heading">Not played in {String(STALE_DAYS)} days</h2>
        {stale.status === "pending" ? (
          <AsyncStatus>Loading stale items…</AsyncStatus>
        ) : stale.status === "error" && stale.data === undefined ? (
          <ReportFailed what="Stale items" onRetry={stale.refetch} />
        ) : (
          <>
            <ItemTiles
              title="Never or long unplayed"
              items={stale.data.pages.flatMap((page) => page.items)}
              empty="Everything here has been played recently."
            />
            <Pager
              noun="stale items"
              hasMore={stale.hasNextPage}
              loading={stale.isFetchingNextPage}
              failed={stale.isFetchNextPageError}
              onMore={stale.fetchNextPage}
            />
          </>
        )}
      </section>
    </>
  );
}

function SortControls({
  sort,
  order,
  onSort,
  onOrder,
}: Readonly<{
  sort: CatalogSort;
  order: CatalogOrder;
  onSort: (sort: CatalogSort) => void;
  onOrder: (order: CatalogOrder) => void;
}>): ReactNode {
  const id = useId();
  return (
    <div className="filter">
      <label htmlFor={`${id}-sort`}>Sort by</label>
      <select
        id={`${id}-sort`}
        value={sort}
        onChange={(event) => {
          const parsed = catalogSortSchema.safeParse(event.target.value);
          if (parsed.success) {
            onSort(parsed.data);
          }
        }}
      >
        {catalogSortSchema.options.map((option) => (
          <option key={option} value={option}>
            {SORT_LABELS[option]}
          </option>
        ))}
      </select>
      <label htmlFor={`${id}-order`}>Order</label>
      <select
        id={`${id}-order`}
        value={order}
        onChange={(event) => {
          const parsed = catalogOrderSchema.safeParse(event.target.value);
          if (parsed.success) {
            onOrder(parsed.data);
          }
        }}
      >
        <option value="desc">Descending</option>
        <option value="asc">Ascending</option>
      </select>
    </div>
  );
}
