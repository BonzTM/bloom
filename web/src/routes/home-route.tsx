import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { VersionBadge } from "../features/system/components/version-badge.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

type Area = Readonly<{
  id: string;
  heading: string;
  summary: string;
  to?: string;
  action?: string;
}>;

const areas: readonly Area[] = [
  {
    id: "users",
    heading: "Users & invites",
    summary:
      "Create invite links, manage media-server accounts, and assign permissions and libraries.",
    to: "/admin/invites",
    action: "Open invites",
  },
  {
    id: "statistics",
    heading: "Statistics",
    summary:
      "See who is watching now, browse playback history, and explore per-user and per-library dashboards.",
    to: "/admin/playback",
    action: "Open playback",
  },
  {
    id: "requests",
    heading: "Requests",
    summary:
      "Discover movies and series, request them with quotas and approval rules, and follow availability.",
    to: "/requests",
    action: "Open requests",
  },
];

export function HomeRoute(): ReactNode {
  usePageTitle(pageTitle("Home"));
  return (
    <>
      <h1>Bloom</h1>
      <p className="page-intro">
        Invites, statistics, and requests for your Jellyfin servers, in one
        place.
      </p>
      <div className="card-grid">
        {areas.map((area) => (
          <AreaCard key={area.id} area={area} />
        ))}
      </div>
      <VersionBadge />
    </>
  );
}

function AreaCard({ area }: Readonly<{ area: Area }>): ReactNode {
  const headingId = `${area.id}-heading`;
  return (
    <section aria-labelledby={headingId} className="card">
      <h2 id={headingId}>{area.heading}</h2>
      <p>{area.summary}</p>
      {area.to === undefined ? (
        <p className="version-badge">Coming soon.</p>
      ) : (
        <p>
          <Link to={area.to}>{area.action}</Link>
        </p>
      )}
    </section>
  );
}
