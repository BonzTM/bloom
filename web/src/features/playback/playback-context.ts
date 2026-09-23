import { createRequiredContext } from "../../lib/react/required-context.js";
import type { PlaybackApi } from "./api/playback-api.js";

const context = createRequiredContext<PlaybackApi>("PlaybackApi");

export const PlaybackApiContext = context.Provider;
export const usePlaybackApi = context.useValue;
