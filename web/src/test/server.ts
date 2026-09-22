import { setupServer } from "msw/node";
import { handlers } from "../mocks/handlers.js";

export const server = setupServer(...handlers);
