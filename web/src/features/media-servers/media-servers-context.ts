import { createRequiredContext } from "../../lib/react/required-context.js";
import type { MediaServersApi } from "./api/media-servers-api.js";

const context = createRequiredContext<MediaServersApi>("MediaServersApi");

export const MediaServersApiContext = context.Provider;
export const useMediaServersApi = context.useValue;
