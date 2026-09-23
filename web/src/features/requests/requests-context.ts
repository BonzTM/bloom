import { createRequiredContext } from "../../lib/react/required-context.js";
import type { RequestsApi } from "./api/requests-api.js";

const context = createRequiredContext<RequestsApi>("RequestsApi");

export const RequestsApiContext = context.Provider;
export const useRequestsApi = context.useValue;
