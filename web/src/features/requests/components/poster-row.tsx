import type { ReactNode } from "react";
import { MediaRow } from "../../../components/media-row.js";
import type {
  MetadataRequestState,
  MetadataTitle,
} from "../api/metadata-schemas.js";
import { TitleCard } from "./title-card.js";

// A title with the caller's request state when the server sent one.
export type RowTitle = MetadataTitle &
  Readonly<{ request_state?: MetadataRequestState | undefined }>;

type PosterRowProps = Readonly<{
  title: string;
  titles: readonly RowTitle[];
  seeAll?: string | undefined;
  // A trailing card such as "More", already wrapped in an <li>.
  trailing?: ReactNode;
  footer?: ReactNode;
}>;

// TMDB titles as one horizontal row of posters, each opening the title's
// page. Any page can use it for a fixed list of titles, such as a title's
// recommendations; the home page's rows wrap it with loading and paging.
export function PosterRow({
  title,
  titles,
  seeAll,
  trailing,
  footer,
}: PosterRowProps): ReactNode {
  return (
    <MediaRow
      title={title}
      seeAll={seeAll}
      footer={footer}
      status={
        titles.length === 0 ? (
          <p className="chart-empty">Nothing to show right now.</p>
        ) : undefined
      }
    >
      {titles.map((item) => (
        <li key={`${item.kind}-${item.provider_id}`}>
          <TitleCard title={item} state={item.request_state} />
        </li>
      ))}
      {trailing}
    </MediaRow>
  );
}
