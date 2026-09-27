import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import {
  formatCount,
  formatDuration,
} from "../../../components/charts/chart-data.js";
import type { CatalogLibrarySummary, Library } from "../api/catalog-schemas.js";
import {
  headlineTypes,
  libraryPath,
  totalPlays,
  totalWatchSeconds,
  typePlural,
} from "./catalog-format.js";

type LibraryCardsProps = Readonly<{
  serverId: string;
  serverName: string;
  libraries: readonly Library[];
  summaries: readonly CatalogLibrarySummary[];
}>;

// One card per library on a server: what it holds, and how much it was
// watched in the window. A library the catalog has not walked yet shows
// with no counts rather than being hidden.
export function LibraryCards({
  serverId,
  serverName,
  libraries,
  summaries,
}: LibraryCardsProps): ReactNode {
  const byId = new Map(summaries.map((s) => [s.library_id, s.types]));
  return (
    <ul className="library-grid" aria-label={`Libraries on ${serverName}`}>
      {libraries.map((library) => {
        const types = byId.get(library.id) ?? [];
        return (
          <li key={library.id} className="library-card">
            <Link
              to={libraryPath(serverId, library.id)}
              className="library-card-name"
            >
              {library.name}
            </Link>
            <dl className="library-card-stats">
              {headlineTypes(types).map((type) => (
                <div key={type.item_type}>
                  <dt>{typePlural(type.item_type)}</dt>
                  <dd>{formatCount(type.items)}</dd>
                </div>
              ))}
              <div>
                <dt>Plays</dt>
                <dd>{formatCount(totalPlays(types))}</dd>
              </div>
              <div>
                <dt>Watch time</dt>
                <dd>{formatDuration(totalWatchSeconds(types))}</dd>
              </div>
            </dl>
            {types.length === 0 ? (
              <p className="row-detail">Not walked yet.</p>
            ) : null}
          </li>
        );
      })}
    </ul>
  );
}
