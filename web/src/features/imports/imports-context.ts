import { createRequiredContext } from "../../lib/react/required-context.js";
import type { ImportsApi } from "./api/imports-api.js";

const context = createRequiredContext<ImportsApi>("ImportsApi");

export const ImportsApiContext = context.Provider;
export const useImportsApi = context.useValue;
