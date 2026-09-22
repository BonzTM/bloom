import type { ReactNode } from "react";
import { VersionBadge } from "../features/system/components/version-badge.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

type Area = Readonly<{ id: string; heading: string; summary: string }>;

const areas: readonly Area[] = [
  {
    id: "users",
    heading: "Users & invites",
    summary:
      "Create invite links, manage media-server accounts, and assign permissions and libraries.",
  },
  {
    id: "statistics",
    heading: "Statistics",
    summary:
      "See who is watching now, browse playback history, and explore per-user and per-library dashboards.",
  },
  {
    id: "requests",
    heading: "Requests",
    summary:
      "Discover movies and series, request them with quotas and approval rules, and follow availability.",
  },
];

export function HomeRoute(): ReactNode {
  usePageTitle(pageTitle("Home"));
  return (
    <>
      <h1>Bloom</h1>
      <VersionBadge />
      {areas.map((area) => (
        <AreaCard key={area.id} area={area} />
      ))}
    </>
  );
}

function AreaCard({ area }: Readonly<{ area: Area }>): ReactNode {
  const headingId = `${area.id}-heading`;
  return (
    <section aria-labelledby={headingId}>
      <h2 id={headingId}>{area.heading}</h2>
      <p>{area.summary}</p>
      <p>Coming soon.</p>
    </section>
  );
}
