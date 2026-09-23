import { createRequiredContext } from "../../lib/react/required-context.js";
import type { RolesApi } from "./api/roles-api.js";

const context = createRequiredContext<RolesApi>("RolesApi");

export const RolesApiContext = context.Provider;
export const useRolesApi = context.useValue;
