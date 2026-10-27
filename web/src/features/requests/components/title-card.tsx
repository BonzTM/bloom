import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { NavIcon, type IconName } from "../../../app/nav-icons.js";
import type {
  MetadataRequestState,
  MetadataTitle,
} from "../api/metadata-schemas.js";
import type { MediaKind } from "../api/requests-schemas.js";
import { Poster } from "./poster.js";
import { kindLabel, titleWithYear } from "./request-format.js";
import { titlePath } from "./title-grid.js";

// What Bloom knows about a title in the library or the requests: available,
// requested and waiting, or on its way. A declined or failed request, or
// none, shows no mark.
export type TitleMark = "available" | "requested" | "processing";

const MARKS: Readonly<
  Record<TitleMark, Readonly<{ label: string; icon: IconName }>>
> = {
  available: { label: "Available", icon: "check" },
  requested: { label: "Requested", icon: "clock" },
  processing: { label: "Processing", icon: "download" },
};

export function titleMark(state: MetadataRequestState): TitleMark | undefined {
  switch (state) {
    case "available":
      return "available";
    case "pending":
      return "requested";
    case "approved":
    case "processing":
      return "processing";
    case "none":
    case "declined":
    case "failed":
      return undefined;
  }
}

// The art of a card: the image, the MOVIE or SERIES badge, and the status
// mark. The badge repeats the kind the card names, so it is hidden from
// assistive technology; the mark's word is read after the title instead.
export function CardArt({
  kind,
  mark,
  children,
}: Readonly<{
  kind: MediaKind;
  mark: TitleMark | undefined;
  children: ReactNode;
}>): ReactNode {
  return (
    <span className="title-card-art">
      {children}
      <span
        className={`title-card-kind title-card-kind-${kind}`}
        aria-hidden="true"
      >
        {kindLabel(kind)}
      </span>
      {mark === undefined ? null : (
        <span
          className={`title-card-mark title-card-mark-${mark}`}
          aria-hidden="true"
          title={MARKS[mark].label}
        >
          <NavIcon name={MARKS[mark].icon} />
        </span>
      )}
    </span>
  );
}

// The words under the art. The kind and the mark are spoken but not shown,
// so a card reads "Heat (1995) Movie" or "Heat (1995) Movie, Available".
export function CardCaption({
  title,
  kind,
  mark,
}: Readonly<{
  title: string;
  kind: MediaKind;
  mark: TitleMark | undefined;
}>): ReactNode {
  return (
    <>
      <span className="poster-card-title">{title}</span>
      <span className="visually-hidden">
        {" "}
        {kindLabel(kind)}
        {mark === undefined ? "" : `, ${MARKS[mark].label}`}
      </span>
    </>
  );
}

// A TMDB title as a poster card that opens the title's page.
export function TitleCard({
  title,
  state = "none",
}: Readonly<{
  title: MetadataTitle;
  state?: MetadataRequestState | undefined;
}>): ReactNode {
  const mark = titleMark(state);
  return (
    <Link to={titlePath(title)} className="poster-card title-card">
      <CardArt kind={title.kind} mark={mark}>
        <Poster
          posterPath={title.poster_path}
          title={title.title}
          size="w342"
        />
      </CardArt>
      <CardCaption title={titleWithYear(title)} kind={title.kind} mark={mark} />
    </Link>
  );
}
