import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { VersionBadge } from "../features/system/components/version-badge.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

export default function AboutRoute(): ReactNode {
  usePageTitle(pageTitle("About"));
  return (
    <article className="card panel">
      <h1>About Bloom</h1>
      <p>
        Bloom is a self-hosted companion for Jellyfin. One binary replaces the
        separate tools an operator runs for invites and user management,
        playback statistics, and media discovery and requests.
      </p>
      <p>
        A <em>bloom</em> is the collective noun for a gathering of jellyfish,
        and the word also means growth, which is what an invite system brings to
        a server.
      </p>
      <VersionBadge />
      <p>
        <Link to="/">Return to home</Link>
      </p>
    </article>
  );
}
