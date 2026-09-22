import { createRequiredContext } from "../../lib/react/required-context.js";
import type { SystemApi } from "./api/system-api.js";

const context = createRequiredContext<SystemApi>("SystemApi");

export const SystemApiContext = context.Provider;
export const useSystemApi = context.useValue;
