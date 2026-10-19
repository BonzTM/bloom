import { setupWorker } from "msw/browser";
import { artworkHandlers } from "./artwork.js";
import { handlers } from "./handlers.js";

// The shared handlers win; artwork only answers the image requests they
// leave alone.
export const worker = setupWorker(...handlers, ...artworkHandlers);
