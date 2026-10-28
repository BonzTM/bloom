import {
  discoverSourcePath,
  type DiscoverSource,
} from "../api/metadata-schemas.js";

// The page that lists every title of a row, a grid at a time.
export function browsePath(source: DiscoverSource): string {
  return `/browse/${discoverSourcePath(source)}`;
}
